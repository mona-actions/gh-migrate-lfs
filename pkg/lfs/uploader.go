package lfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBatchSize   = 100
	defaultParallel    = 8
	defaultRetryMax    = 6
	maxResponseBody    = 64 * 1024 * 1024
	maxErrorBody       = 500
	maxUploadWorkers   = 512
	maxObjectsPerBatch = 10000
)

type Config struct {
	Endpoint   string
	Token      string
	BatchSize  int
	Parallel   int
	RetryMax   int
	RetryDelay time.Duration
	HTTPClient *http.Client
	Reporter   IssueReporter
}

type Object struct {
	OID  string
	Size int64
	Path string
}

type Uploader struct {
	endpoint   *url.URL
	batchURL   string
	authHeader string
	batchSize  int
	parallel   int
	retryMax   int
	retryDelay time.Duration
	httpClient *http.Client
	reporter   IssueReporter
}

type batchRequest struct {
	Operation string             `json:"operation"`
	Transfers []string           `json:"transfers"`
	HashAlgo  string             `json:"hash_algo"`
	Objects   []batchRequestItem `json:"objects"`
}

type batchRequestItem struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

type batchResponse struct {
	Objects []batchResponseObject `json:"objects"`
}

type batchResponseObject struct {
	OID     string            `json:"oid"`
	Actions map[string]action `json:"actions,omitempty"`
	Error   *lfsError         `json:"error,omitempty"`
}

type action struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header,omitempty"`
}

type lfsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type uploadJob struct {
	object Object
	upload action
	verify *action
}

type uploadResult struct {
	oid string
	err error
}

type negotiationResult struct {
	jobs         []uploadJob
	present      int
	serverErrors int
	unexpected   int
	issues       []Issue
}

type verificationResult struct {
	object Object
	err    error
}

type batchProcessError struct {
	err error
}

func (processError batchProcessError) Error() string {
	return processError.err.Error()
}

func (processError batchProcessError) Unwrap() error {
	return processError.err
}

func NewUploader(cfg Config) (*Uploader, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("invalid LFS endpoint %q", cfg.Endpoint)
	}
	if parsedEndpoint.User != nil {
		return nil, errors.New("LFS endpoint must not contain credentials")
	}
	if parsedEndpoint.Scheme != "https" && parsedEndpoint.Hostname() != "localhost" {
		return nil, errors.New("LFS endpoint must use HTTPS")
	}

	batchSize := cfg.BatchSize
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}
	if batchSize < 1 || batchSize > maxObjectsPerBatch {
		return nil, fmt.Errorf("batch size must be between 1 and %d", maxObjectsPerBatch)
	}

	parallel := cfg.Parallel
	if parallel == 0 {
		parallel = defaultParallel
	}
	if parallel < 1 || parallel > maxUploadWorkers {
		return nil, fmt.Errorf("parallel uploads must be between 1 and %d", maxUploadWorkers)
	}

	retryMax := cfg.RetryMax
	if retryMax == 0 {
		retryMax = defaultRetryMax
	}
	if retryMax < 1 {
		return nil, errors.New("retry max must be at least 1")
	}

	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = time.Second
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:          max(100, parallel*2),
				MaxIdleConnsPerHost:   max(20, parallel),
				MaxConnsPerHost:       max(20, parallel+4),
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   20 * time.Second,
				ExpectContinueTimeout: time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		}
	}

	authHeader := ""
	if cfg.Token != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + cfg.Token))
		authHeader = "Basic " + credentials
	}

	return &Uploader{
		endpoint:   parsedEndpoint,
		batchURL:   endpoint + "/objects/batch",
		authHeader: authHeader,
		batchSize:  batchSize,
		parallel:   parallel,
		retryMax:   retryMax,
		retryDelay: retryDelay,
		httpClient: httpClient,
		reporter:   cfg.Reporter,
	}, nil
}

func EndpointURL(host, organization, repository string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "github.com"
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}

	parsed, err := url.Parse(host)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid target hostname %q", host)
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
		return "", errors.New("target hostname must use HTTPS")
	}
	hostPath := strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/api/v3")
	if hostPath != "" {
		return "", fmt.Errorf("target hostname must not contain path %q", hostPath)
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil

	organization = strings.TrimSpace(organization)
	repository = strings.TrimSuffix(strings.TrimSpace(repository), ".git")
	if organization == "" || repository == "" || strings.ContainsAny(organization+repository, "/\\") {
		return "", errors.New("target organization and repository must each be one path segment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + organization + "/" + repository + ".git/info/lfs"
	return strings.TrimRight(parsed.String(), "/"), nil
}

func WalkObjectBatches(ctx context.Context, repoPath string, batchSize int, process func([]Object) error) (int, error) {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if batchSize > maxObjectsPerBatch {
		return 0, fmt.Errorf("batch size must not exceed %d", maxObjectsPerBatch)
	}
	if process == nil {
		return 0, errors.New("object batch processor is required")
	}
	candidates := []string{
		filepath.Join(repoPath, ".git", "lfs", "objects"),
		filepath.Join(repoPath, "lfs", "objects"),
	}

	var objectRoot string
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			objectRoot = candidate
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("inspect LFS object store: %w", err)
		}
	}
	if objectRoot == "" {
		return 0, fmt.Errorf("LFS object store not found under %s", repoPath)
	}

	batch := make([]Object, 0, batchSize)
	total := 0
	err := filepath.WalkDir(objectRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !isOID(entry.Name()) {
			return nil
		}
		relativePath, err := filepath.Rel(objectRoot, path)
		if err != nil {
			return err
		}
		oid := entry.Name()
		if filepath.Clean(relativePath) != filepath.Join(oid[:2], oid[2:4], oid) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("LFS object is not a regular file: %s", path)
		}
		batch = append(batch, Object{OID: oid, Size: info.Size(), Path: path})
		total++
		if len(batch) == batchSize {
			if err := process(batch); err != nil {
				return batchProcessError{err: err}
			}
			batch = make([]Object, 0, batchSize)
		}
		return nil
	})
	if err != nil {
		var processError batchProcessError
		if errors.As(err, &processError) {
			return total, processError.err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return total, err
		}
		return total, fmt.Errorf("scan LFS object store: %w", err)
	}
	if len(batch) > 0 {
		if err := process(batch); err != nil {
			return total, err
		}
	}
	return total, nil
}

func VerifyObjects(ctx context.Context, objects []Object, parallel int, reporter IssueReporter) ([]Object, error) {
	if parallel < 1 {
		parallel = 1
	}
	jobs := make(chan Object)
	results := make(chan verificationResult, parallel)
	var workers sync.WaitGroup

	for range min(parallel, len(objects)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for object := range jobs {
				err := verifyObject(ctx, object)
				if err != nil {
					if reporter != nil {
						reporter.ReportIssue(Issue{OID: object.OID, Stage: "local-hash", Message: err.Error()})
					}
				}
				results <- verificationResult{object: object, err: err}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, object := range objects {
			select {
			case <-ctx.Done():
				return
			case jobs <- object:
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	verified := make([]Object, 0, len(objects))
	var verificationErrors []error
	for result := range results {
		if result.err != nil {
			verificationErrors = append(verificationErrors, result.err)
		} else {
			verified = append(verified, result.object)
		}
	}
	if err := ctx.Err(); err != nil {
		verificationErrors = append(verificationErrors, err)
	}
	return verified, errors.Join(verificationErrors...)
}

func (u *Uploader) Upload(ctx context.Context, objects []Object, dryRun bool) (Stats, error) {
	stats := Stats{Objects: len(objects)}
	var uploadErrors []error

	for start := 0; start < len(objects); start += u.batchSize {
		if err := ctx.Err(); err != nil {
			return stats, errors.Join(append(uploadErrors, err)...)
		}
		end := min(start+u.batchSize, len(objects))
		chunk := objects[start:end]
		result, err := u.negotiate(ctx, chunk)
		if err != nil {
			stats.BatchFailures += len(chunk)
			for _, object := range chunk {
				u.report(Issue{OID: object.OID, Stage: "batch", Message: err.Error()})
			}
			uploadErrors = append(uploadErrors, err)
			continue
		}
		stats.AlreadyPresent += result.present
		stats.WouldUpload += len(result.jobs)
		stats.ServerErrors += result.serverErrors
		stats.Unexpected += result.unexpected
		for _, issue := range result.issues {
			u.report(issue)
			uploadErrors = append(uploadErrors, errors.New(issue.Message))
		}
		if dryRun {
			continue
		}

		for upload := range u.uploadJobs(ctx, result.jobs) {
			if upload.err == nil {
				stats.Uploaded++
				continue
			}
			stats.UploadFailures++
			u.report(Issue{OID: upload.oid, Stage: "upload", Message: upload.err.Error()})
			uploadErrors = append(uploadErrors, upload.err)
		}
		if err := ctx.Err(); err != nil {
			uploadErrors = append(uploadErrors, err)
			return stats, errors.Join(uploadErrors...)
		}
	}

	return stats, errors.Join(uploadErrors...)
}

func (u *Uploader) Reconcile(ctx context.Context, objects []Object) (Stats, error) {
	stats := Stats{Objects: len(objects)}
	var reconciliationErrors []error
	for start := 0; start < len(objects); start += u.batchSize {
		if err := ctx.Err(); err != nil {
			return stats, errors.Join(append(reconciliationErrors, err)...)
		}
		end := min(start+u.batchSize, len(objects))
		chunk := objects[start:end]
		result, err := u.negotiate(ctx, chunk)
		if err != nil {
			stats.RemoteErrors += len(chunk)
			for _, object := range chunk {
				u.report(Issue{OID: object.OID, Stage: "reconcile", Message: err.Error()})
			}
			reconciliationErrors = append(reconciliationErrors, err)
			continue
		}
		stats.RemotePresent += result.present
		stats.RemoteErrors += result.serverErrors + result.unexpected
		for _, issue := range result.issues {
			issue.Stage = "reconcile"
			u.report(issue)
			reconciliationErrors = append(reconciliationErrors, errors.New(issue.Message))
		}
		for _, job := range result.jobs {
			stats.RemoteMissing++
			u.report(Issue{OID: job.object.OID, Stage: "reconcile", Message: "object still missing remotely"})
			reconciliationErrors = append(reconciliationErrors, fmt.Errorf("object %s is still missing remotely", job.object.OID))
		}
	}
	return stats, errors.Join(reconciliationErrors...)
}

func (u *Uploader) negotiate(ctx context.Context, objects []Object) (negotiationResult, error) {
	requestBody := batchRequest{
		Operation: "upload",
		Transfers: []string{"basic"},
		HashAlgo:  "sha256",
		Objects:   make([]batchRequestItem, 0, len(objects)),
	}
	expected := make(map[string]Object, len(objects))
	for _, object := range objects {
		requestBody.Objects = append(requestBody.Objects, batchRequestItem{OID: object.OID, Size: object.Size})
		expected[object.OID] = object
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return negotiationResult{}, err
	}

	headers := map[string]string{
		"Accept":       "application/vnd.git-lfs+json",
		"Content-Type": "application/vnd.git-lfs+json",
	}
	if u.authHeader != "" {
		headers["Authorization"] = u.authHeader
	}
	status, responseBody, err := u.doWithRetry(ctx, http.MethodPost, u.batchURL, headers, int64(len(body)), func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	})
	if err != nil {
		return negotiationResult{}, fmt.Errorf("batch negotiation failed: %w", err)
	}
	if status < 200 || status >= 300 {
		return negotiationResult{}, fmt.Errorf("batch negotiation HTTP %d: %s", status, truncate(responseBody, maxErrorBody))
	}

	var response batchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return negotiationResult{}, fmt.Errorf("invalid batch response: %w", err)
	}

	accounted := make(map[string]struct{}, len(objects))
	result := negotiationResult{jobs: make([]uploadJob, 0, len(objects))}
	for _, responseObject := range response.Objects {
		object, ok := expected[responseObject.OID]
		if !ok {
			result.unexpected++
			result.issues = append(result.issues, Issue{OID: responseObject.OID, Stage: "batch-response", Message: "server returned an unrequested object"})
			continue
		}
		if _, duplicate := accounted[responseObject.OID]; duplicate {
			result.unexpected++
			result.issues = append(result.issues, Issue{OID: responseObject.OID, Stage: "batch-response", Message: "server returned a duplicate object"})
			continue
		}
		accounted[responseObject.OID] = struct{}{}
		if responseObject.Error != nil {
			result.serverErrors++
			result.issues = append(result.issues, Issue{OID: responseObject.OID, Stage: "server", Message: fmt.Sprintf("server error %d: %s", responseObject.Error.Code, responseObject.Error.Message)})
			continue
		}

		upload, needsUpload := responseObject.Actions["upload"]
		if !needsUpload || upload.Href == "" {
			result.present++
			continue
		}
		var verify *action
		if verifyAction, ok := responseObject.Actions["verify"]; ok && verifyAction.Href != "" {
			verify = &verifyAction
		}
		result.jobs = append(result.jobs, uploadJob{object: object, upload: upload, verify: verify})
	}
	for _, object := range objects {
		if _, ok := accounted[object.OID]; !ok {
			result.unexpected++
			result.issues = append(result.issues, Issue{OID: object.OID, Stage: "batch-response", Message: "object missing from batch response"})
		}
	}

	return result, nil
}

func (u *Uploader) uploadJobs(ctx context.Context, jobs []uploadJob) <-chan uploadResult {
	results := make(chan uploadResult)
	if len(jobs) == 0 {
		close(results)
		return results
	}
	input := make(chan uploadJob)
	var workers sync.WaitGroup

	for range min(u.parallel, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range input {
				results <- uploadResult{oid: job.object.OID, err: u.uploadOne(ctx, job)}
			}
		}()
	}
	go func() {
		defer close(input)
		for _, job := range jobs {
			select {
			case <-ctx.Done():
				return
			case input <- job:
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	return results
}

func (u *Uploader) uploadOne(ctx context.Context, job uploadJob) error {
	if err := validateTransferURL(job.upload.Href); err != nil {
		return fmt.Errorf("upload object %s: %w", job.object.OID, err)
	}
	headers := cloneHeaders(job.upload.Header)
	headers["Content-Type"] = "application/octet-stream"
	if u.shouldAttachAuth(job.upload.Href, headers) {
		headers["Authorization"] = u.authHeader
	}
	status, body, err := u.doWithRetry(ctx, http.MethodPut, job.upload.Href, headers, job.object.Size, func() (io.ReadCloser, error) {
		return os.Open(job.object.Path)
	})
	if err != nil {
		return fmt.Errorf("upload object %s: %w", job.object.OID, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("upload object %s: HTTP %d: %s", job.object.OID, status, truncate(body, maxErrorBody))
	}

	if job.verify == nil {
		return nil
	}
	if err := validateTransferURL(job.verify.Href); err != nil {
		return fmt.Errorf("verify object %s: %w", job.object.OID, err)
	}
	verifyBody, err := json.Marshal(batchRequestItem{OID: job.object.OID, Size: job.object.Size})
	if err != nil {
		return err
	}
	headers = cloneHeaders(job.verify.Header)
	headers["Accept"] = "application/vnd.git-lfs+json"
	headers["Content-Type"] = "application/vnd.git-lfs+json"
	if u.shouldAttachAuth(job.verify.Href, headers) {
		headers["Authorization"] = u.authHeader
	}
	status, body, err = u.doWithRetry(ctx, http.MethodPost, job.verify.Href, headers, int64(len(verifyBody)), func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(verifyBody)), nil
	})
	if err != nil {
		return fmt.Errorf("verify object %s: %w", job.object.OID, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("verify object %s: HTTP %d: %s", job.object.OID, status, truncate(body, maxErrorBody))
	}
	return nil
}

func (u *Uploader) doWithRetry(ctx context.Context, method, rawURL string, headers map[string]string, contentLength int64, body func() (io.ReadCloser, error)) (int, []byte, error) {
	var lastErr error
	for attempt := 1; attempt <= u.retryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		requestBody, err := body()
		if err != nil {
			return 0, nil, err
		}
		request, err := http.NewRequestWithContext(ctx, method, rawURL, requestBody)
		if err != nil {
			requestBody.Close()
			return 0, nil, err
		}
		request.ContentLength = contentLength
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		request.Header.Set("User-Agent", "gh-migrate-lfs")

		response, err := u.httpClient.Do(request)
		requestBody.Close()
		if err != nil {
			lastErr = err
			if attempt < u.retryMax && sleepRetry(ctx, u.retryDelay, attempt, 0) {
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 0, nil, ctxErr
			}
			return 0, nil, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < u.retryMax && sleepRetry(ctx, u.retryDelay, attempt, 0) {
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 0, nil, ctxErr
			}
			return 0, nil, readErr
		}
		if len(responseBody) > maxResponseBody {
			return 0, nil, fmt.Errorf("HTTP response exceeds %d bytes", maxResponseBody)
		}
		if retryableStatus(response.StatusCode) && attempt < u.retryMax {
			retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
			if sleepRetry(ctx, u.retryDelay, attempt, retryAfter) {
				continue
			}
			return 0, nil, ctx.Err()
		}
		return response.StatusCode, responseBody, nil
	}
	return 0, nil, lastErr
}

func (u *Uploader) shouldAttachAuth(actionURL string, headers map[string]string) bool {
	if u.authHeader == "" {
		return false
	}
	for name := range headers {
		if strings.EqualFold(name, "Authorization") {
			return false
		}
	}
	actionEndpoint, err := url.Parse(actionURL)
	return err == nil && strings.EqualFold(u.endpoint.Host, actionEndpoint.Host)
}

func (u *Uploader) report(issue Issue) {
	if u.reporter != nil {
		u.reporter.ReportIssue(issue)
	}
}

func validateTransferURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid transfer URL %q", rawURL)
	}
	if parsed.User != nil {
		return errors.New("transfer URL must not contain credentials")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
		return errors.New("transfer URL must use HTTPS")
	}
	return nil
}

func verifyObject(ctx context.Context, object Object) error {
	file, err := os.Open(object.Path)
	if err != nil {
		return fmt.Errorf("verify object %s: %w", object.OID, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return fmt.Errorf("verify object %s: %w", object.OID, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != object.OID {
		return fmt.Errorf("verify object %s: hash mismatch, got %s", object.OID, actual)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func isOID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func cloneHeaders(headers map[string]string) map[string]string {
	clone := make(map[string]string, len(headers)+2)
	for name, value := range headers {
		clone[name] = value
	}
	return clone
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status == 425 || status >= 500
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return max(time.Until(retryAt), 0)
	}
	return 0
}

func sleepRetry(ctx context.Context, baseDelay time.Duration, attempt int, retryAfter time.Duration) bool {
	delay := retryAfter
	if delay <= 0 {
		delay = baseDelay * time.Duration(1<<(attempt-1))
		delay = min(delay, 16*time.Second)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func truncate(value []byte, limit int) string {
	text := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(string(value))
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
