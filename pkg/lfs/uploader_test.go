package lfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpointURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "github.com default", want: "https://github.com/octo/repo.git/info/lfs"},
		{name: "GHES hostname", host: "github.example.com", want: "https://github.example.com/octo/repo.git/info/lfs"},
		{name: "normalized GHES API URL", host: "https://github.example.com/api/v3", want: "https://github.example.com/octo/repo.git/info/lfs"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := EndpointURL(test.host, "octo", "repo")
			if err != nil {
				t.Fatalf("Endpoint() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Endpoint() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWalkObjectBatches(t *testing.T) {
	t.Parallel()

	content := []byte("local LFS object")
	oid := oidFor(content)
	for _, objectStore := range []string{filepath.Join("lfs", "objects"), filepath.Join(".git", "lfs", "objects")} {
		objectStore := objectStore
		t.Run(objectStore, func(t *testing.T) {
			t.Parallel()
			repoPath := t.TempDir()
			objectPath := filepath.Join(repoPath, objectStore, oid[:2], oid[2:4], oid)
			if err := os.MkdirAll(filepath.Dir(objectPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(objectPath, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(objectPath), "tmp-file"), content, 0o600); err != nil {
				t.Fatal(err)
			}

			objects, total, err := collectObjectBatches(repoPath, 1)
			if err != nil {
				t.Fatalf("WalkObjectBatches() error = %v", err)
			}
			if total != 1 || len(objects) != 1 || objects[0].OID != oid || objects[0].Size != int64(len(content)) {
				t.Fatalf("WalkObjectBatches() total = %d, objects = %#v", total, objects)
			}
		})
	}
}

func TestUploaderUploadAndReconcile(t *testing.T) {
	t.Parallel()

	content := []byte("upload me directly")
	oid := oidFor(content)
	objectPath := filepath.Join(t.TempDir(), oid)
	if err := os.WriteFile(objectPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	var server *httptest.Server
	var batchCalls atomic.Int32
	var uploadCalls atomic.Int32
	var verifyCalls atomic.Int32
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/octo/repo.git/info/lfs/objects/batch":
			call := batchCalls.Add(1)
			if call == 1 {
				response.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			assertBasicAuth(t, request, "x-access-token", "secret")
			response.Header().Set("Content-Type", "application/vnd.git-lfs+json")
			if call == 2 {
				fmt.Fprintf(response, `{"objects":[{"oid":%q,"actions":{"upload":{"href":%q,"header":{"X-Upload":"allowed"}},"verify":{"href":%q}}}]}`, oid, server.URL+"/upload", server.URL+"/verify")
				return
			}
			fmt.Fprintf(response, `{"objects":[{"oid":%q}]}`, oid)
		case "/upload":
			uploadCalls.Add(1)
			assertBasicAuth(t, request, "x-access-token", "secret")
			if request.Header.Get("X-Upload") != "allowed" {
				t.Errorf("upload header = %q", request.Header.Get("X-Upload"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read upload: %v", err)
			}
			if string(body) != string(content) {
				t.Errorf("upload body = %q, want %q", body, content)
			}
			response.WriteHeader(http.StatusOK)
		case "/verify":
			verifyCalls.Add(1)
			assertBasicAuth(t, request, "x-access-token", "secret")
			var item batchRequestItem
			if err := json.NewDecoder(request.Body).Decode(&item); err != nil {
				t.Errorf("decode verify request: %v", err)
			}
			if item.OID != oid || item.Size != int64(len(content)) {
				t.Errorf("verify item = %#v", item)
			}
			response.WriteHeader(http.StatusOK)
		default:
			http.NotFound(response, request)
		}
	})
	server = httptest.NewTLSServer(handler)
	defer server.Close()

	uploader, err := NewUploader(Config{
		Endpoint:   server.URL + "/octo/repo.git/info/lfs",
		Token:      "secret",
		BatchSize:  100,
		Parallel:   2,
		RetryMax:   2,
		RetryDelay: time.Millisecond,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewUploader() error = %v", err)
	}
	objects := []Object{{OID: oid, Size: int64(len(content)), Path: objectPath}}
	stats, err := uploader.Upload(context.Background(), objects, false)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if stats.Objects != 1 || stats.Uploaded != 1 || stats.AlreadyPresent != 0 {
		t.Fatalf("Upload() stats = %#v", stats)
	}
	reconcileStats, err := uploader.Reconcile(context.Background(), objects)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if reconcileStats.RemotePresent != 1 || reconcileStats.RemoteMissing != 0 || reconcileStats.RemoteErrors != 0 {
		t.Fatalf("Reconcile() stats = %#v", reconcileStats)
	}
	if batchCalls.Load() != 3 || uploadCalls.Load() != 1 || verifyCalls.Load() != 1 {
		t.Fatalf("calls: batch=%d upload=%d verify=%d", batchCalls.Load(), uploadCalls.Load(), verifyCalls.Load())
	}
}

func TestUploaderReportsMixedBatchOutcomes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	uploadContent := []byte("upload")
	uploadOID := oidFor(uploadContent)
	uploadPath := filepath.Join(root, uploadOID)
	if err := os.WriteFile(uploadPath, uploadContent, 0o600); err != nil {
		t.Fatal(err)
	}
	serverOID := strings.Repeat("b", 64)
	missingOID := strings.Repeat("c", 64)

	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repo.git/info/lfs/objects/batch":
			fmt.Fprintf(response, `{"objects":[{"oid":%q,"actions":{"upload":{"href":%q}}},{"oid":%q,"error":{"code":403,"message":"denied"}}]}`, uploadOID, server.URL+"/upload", serverOID)
		case "/upload":
			response.WriteHeader(http.StatusOK)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	var issues []Issue
	uploader, err := NewUploader(Config{
		Endpoint:   server.URL + "/repo.git/info/lfs",
		HTTPClient: server.Client(),
		RetryMax:   1,
		Reporter: IssueReporterFunc(func(issue Issue) {
			issues = append(issues, issue)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := []Object{
		{OID: uploadOID, Size: int64(len(uploadContent)), Path: uploadPath},
		{OID: serverOID, Size: 1, Path: filepath.Join(root, serverOID)},
		{OID: missingOID, Size: 1, Path: filepath.Join(root, missingOID)},
	}
	stats, err := uploader.Upload(context.Background(), objects, false)
	if err == nil {
		t.Fatal("Upload() returned no error")
	}
	if stats.Objects != 3 || stats.WouldUpload != 1 || stats.Uploaded != 1 || stats.ServerErrors != 1 || stats.Unexpected != 1 {
		t.Fatalf("Upload() stats = %#v", stats)
	}
	if len(issues) != 2 || issues[0].OID != serverOID || issues[1].OID != missingOID {
		t.Fatalf("Upload() issues = %#v", issues)
	}
}

func TestShouldAttachAuth(t *testing.T) {
	t.Parallel()

	uploader, err := NewUploader(Config{Endpoint: "https://github.example.com/org/repo.git/info/lfs", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !uploader.shouldAttachAuth("https://github.example.com/upload", nil) {
		t.Fatal("expected auth on the endpoint host")
	}
	if uploader.shouldAttachAuth("https://objects.example.net/upload", nil) {
		t.Fatal("auth must not be attached to a different host")
	}
	if uploader.shouldAttachAuth("https://github.example.com/upload", map[string]string{"authorization": "signed"}) {
		t.Fatal("server-provided authorization must not be replaced")
	}
}

func TestValidateTransferURL(t *testing.T) {
	t.Parallel()

	if err := validateTransferURL("https://objects.example.com/upload?signature=value"); err != nil {
		t.Fatalf("validateTransferURL() error = %v", err)
	}
	for _, rawURL := range []string{
		"http://objects.example.com/upload",
		"https://token@objects.example.com/upload",
		"/relative/upload",
	} {
		if err := validateTransferURL(rawURL); err == nil {
			t.Errorf("validateTransferURL(%q) returned no error", rawURL)
		}
	}
}

func TestReconcileHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	uploader, err := NewUploader(Config{Endpoint: "https://github.example.com/octo/repo.git/info/lfs"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uploader.Reconcile(ctx, []Object{{OID: strings.Repeat("0", 64), Size: 1}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

func TestRetryBackoffReturnsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	uploader, err := NewUploader(Config{
		Endpoint:   "https://github.example.com/octo/repo.git/info/lfs",
		RetryMax:   2,
		RetryDelay: time.Minute,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, errors.New("transport failed")
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = uploader.doWithRetry(ctx, http.MethodPost, "https://github.example.com/upload", nil, 0, func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("")), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("doWithRetry() error = %v", err)
	}
}

func TestUploadCancellationStopsWorkers(t *testing.T) {
	t.Parallel()

	content := []byte("cancel upload")
	oid := oidFor(content)
	path := filepath.Join(t.TempDir(), oid)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	uploadStarted := make(chan struct{})
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/repo.git/info/lfs/objects/batch" {
			body := fmt.Sprintf(`{"objects":[{"oid":%q,"actions":{"upload":{"href":"https://github.example.com/upload"}}}]}`, oid)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		if request.URL.Path == "/upload" {
			close(uploadStarted)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
	})
	uploader, err := NewUploader(Config{
		Endpoint:   "https://github.example.com/repo.git/info/lfs",
		HTTPClient: &http.Client{Transport: transport},
		RetryMax:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := uploader.Upload(ctx, []Object{{OID: oid, Size: int64(len(content)), Path: path}}, false)
		done <- err
	}()
	<-uploadStarted
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Upload() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upload() did not stop after cancellation")
	}
}

func TestWalkObjectBatchesPrefersNormalCloneStore(t *testing.T) {
	t.Parallel()

	repoPath := t.TempDir()
	normalContent := []byte("normal clone")
	bareContent := []byte("bare clone")
	normalOID := writeObject(t, filepath.Join(repoPath, ".git", "lfs", "objects"), normalContent)
	writeObject(t, filepath.Join(repoPath, "lfs", "objects"), bareContent)

	objects, _, err := collectObjectBatches(repoPath, 1)
	if err != nil {
		t.Fatalf("WalkObjectBatches() error = %v", err)
	}
	if len(objects) != 1 || objects[0].OID != normalOID {
		t.Fatalf("WalkObjectBatches() = %#v", objects)
	}
}

func TestWalkObjectBatchesBoundsBatchSize(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "lfs", "objects")
	for index := range 5 {
		writeObject(t, root, []byte(fmt.Sprintf("object-%d", index)))
	}
	var batchSizes []int
	total, err := WalkObjectBatches(context.Background(), filepath.Dir(filepath.Dir(root)), 2, func(objects []Object) error {
		batchSizes = append(batchSizes, len(objects))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || fmt.Sprint(batchSizes) != "[2 2 1]" {
		t.Fatalf("total = %d, batch sizes = %v", total, batchSizes)
	}
}

func TestWalkObjectBatchesPreservesProcessorError(t *testing.T) {
	t.Parallel()

	repositoryPath := t.TempDir()
	writeObject(t, filepath.Join(repositoryPath, "lfs", "objects"), []byte("object"))
	want := errors.New("processor failed")
	_, err := WalkObjectBatches(context.Background(), repositoryPath, 1, func([]Object) error {
		return want
	})
	if !errors.Is(err, want) || strings.Contains(err.Error(), "scan LFS object store") {
		t.Fatalf("WalkObjectBatches() error = %v", err)
	}
}

func TestWalkObjectBatchesHonorsCancellation(t *testing.T) {
	t.Parallel()

	repositoryPath := t.TempDir()
	writeObject(t, filepath.Join(repositoryPath, "lfs", "objects"), []byte("object"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := WalkObjectBatches(ctx, repositoryPath, 1, func([]Object) error {
		t.Fatal("processor called after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WalkObjectBatches() error = %v", err)
	}
	if strings.Contains(err.Error(), "scan LFS object store") {
		t.Fatalf("WalkObjectBatches() mislabeled cancellation: %v", err)
	}
}

func TestVerifyObjectsRejectsCorruption(t *testing.T) {
	t.Parallel()

	oid := strings.Repeat("0", 64)
	path := filepath.Join(t.TempDir(), oid)
	if err := os.WriteFile(path, []byte("not zero"), 0o600); err != nil {
		t.Fatal(err)
	}
	var issues []Issue
	verified, err := VerifyObjects(context.Background(), []Object{{OID: oid, Path: path}}, 1, IssueReporterFunc(func(issue Issue) {
		issues = append(issues, issue)
	}))
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("VerifyObjects() error = %v", err)
	}
	if len(issues) != 1 || issues[0].OID != oid || issues[0].Stage != "local-hash" {
		t.Fatalf("VerifyObjects() issues = %#v", issues)
	}
	if len(verified) != 0 {
		t.Fatalf("VerifyObjects() verified = %#v", verified)
	}
}

func assertBasicAuth(t *testing.T, request *http.Request, wantUser, wantPassword string) {
	t.Helper()
	user, password, ok := request.BasicAuth()
	if !ok || user != wantUser || password != wantPassword {
		t.Errorf("basic auth = (%q, %q, %t)", user, password, ok)
	}
}

func oidFor(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func writeObject(t *testing.T, root string, content []byte) string {
	t.Helper()
	oid := oidFor(content)
	path := filepath.Join(root, oid[:2], oid[2:4], oid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return oid
}

func collectObjectBatches(repoPath string, batchSize int) ([]Object, int, error) {
	var objects []Object
	total, err := WalkObjectBatches(context.Background(), repoPath, batchSize, func(batch []Object) error {
		objects = append(objects, batch...)
		return nil
	})
	return objects, total, err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
