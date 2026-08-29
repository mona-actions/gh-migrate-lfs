package migrate

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	exportpkg "github.com/mona-actions/gh-migrate-lfs/pkg/export"
	"github.com/mona-actions/gh-migrate-lfs/pkg/pull"
	syncpkg "github.com/mona-actions/gh-migrate-lfs/pkg/sync"
)

func TestRunExecutesAllPhasesInOrder(t *testing.T) {
	t.Parallel()

	workDir := filepath.Join(t.TempDir(), "work")
	wantManifest := filepath.Join(workDir, "source_lfs.csv")
	var order []string
	var pullManifest string
	var syncManifest string
	phases := phaseRunner{
		export: func(_ context.Context, cfg exportpkg.Config) error {
			order = append(order, "export")
			if cfg.OutputFile != wantManifest {
				t.Fatalf("export manifest = %q, want %q", cfg.OutputFile, wantManifest)
			}
			return nil
		},
		pull: func(_ context.Context, cfg pull.Config) error {
			order = append(order, "pull")
			pullManifest = cfg.InputFile
			return nil
		},
		sync: func(_ context.Context, cfg syncpkg.Config) error {
			order = append(order, "sync")
			syncManifest = cfg.InputFile
			return nil
		},
	}

	err := run(context.Background(), validConfig(workDir), phases)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"export", "pull", "sync"}) {
		t.Fatalf("phase order = %v", order)
	}
	if pullManifest != wantManifest || syncManifest != wantManifest {
		t.Fatalf("manifest handoff: pull=%q sync=%q", pullManifest, syncManifest)
	}
}

func TestRunUsesExistingManifest(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	manifestPath := filepath.Join(workDir, "curated.csv")
	cfg := validConfig(workDir)
	cfg.Manifest = manifestPath
	cfg.SourceOrganization = ""
	var exported bool
	phases := phaseRunner{
		export: func(context.Context, exportpkg.Config) error {
			exported = true
			return nil
		},
		pull: func(_ context.Context, cfg pull.Config) error {
			if cfg.InputFile != manifestPath {
				t.Fatalf("pull manifest = %q", cfg.InputFile)
			}
			return nil
		},
		sync: func(_ context.Context, cfg syncpkg.Config) error {
			if cfg.InputFile != manifestPath {
				t.Fatalf("sync manifest = %q", cfg.InputFile)
			}
			return nil
		},
	}

	if err := run(context.Background(), cfg, phases); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exported {
		t.Fatal("export phase ran with an existing manifest")
	}
}

func TestRunStopsAfterPhaseFailure(t *testing.T) {
	t.Parallel()

	phaseError := errors.New("phase failed")
	tests := []struct {
		name      string
		exportErr error
		pullErr   error
		syncErr   error
		wantOrder []string
	}{
		{name: "export", exportErr: phaseError, wantOrder: []string{"export"}},
		{name: "pull", pullErr: phaseError, wantOrder: []string{"export", "pull"}},
		{name: "sync", syncErr: phaseError, wantOrder: []string{"export", "pull", "sync"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var order []string
			phases := phaseRunner{
				export: func(context.Context, exportpkg.Config) error {
					order = append(order, "export")
					return test.exportErr
				},
				pull: func(context.Context, pull.Config) error {
					order = append(order, "pull")
					return test.pullErr
				},
				sync: func(context.Context, syncpkg.Config) error {
					order = append(order, "sync")
					return test.syncErr
				},
			}
			err := run(context.Background(), validConfig(filepath.Join(t.TempDir(), "work")), phases)
			if !errors.Is(err, phaseError) {
				t.Fatalf("run() error = %v", err)
			}
			if !reflect.DeepEqual(order, test.wantOrder) {
				t.Fatalf("phase order = %v, want %v", order, test.wantOrder)
			}
		})
	}
}

func TestValidateConfigAllowsManifestWithoutSourceOrganization(t *testing.T) {
	t.Parallel()

	cfg := validConfig("work")
	cfg.Manifest = "repositories.csv"
	cfg.SourceOrganization = ""
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("validateConfig() error = %v", err)
	}
}

func TestValidateConfigRequiresSourceOrganizationForExport(t *testing.T) {
	t.Parallel()

	cfg := validConfig("work")
	cfg.SourceOrganization = ""
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() returned no error")
	}
}

func validConfig(workDir string) Config {
	return Config{
		SourceOrganization: "source",
		SourceToken:        "source-token",
		TargetOrganization: "target",
		TargetToken:        "target-token",
		WorkDir:            workDir,
	}
}

func TestValidateConfigRejectsTransferLimits(t *testing.T) {
	t.Parallel()

	cfg := validConfig("work")
	cfg.BatchSize = maxBatchSize + 1
	cfg.UploadParallel = maxUploadParallel + 1
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() returned no error")
	}
}
