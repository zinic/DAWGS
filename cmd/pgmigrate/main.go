package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/specterops/dawgs/drivers/pg/migrate"
)

var (
	version        = "2.0.0-dev"
	sourceRevision = "unknown"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: dawgs-pg-migrate <status|bootstrap|complete-bootstrap|preflight|confirm|migrate|verify-fence|resume|validate|accept|smoke|rollback|complete-v1-handoff|rebind-restore|cleanup|receipt|health|validate-fleet|validate-archive|version>")
	}
	if args[0] == "version" {
		_, err := fmt.Fprintf(stdout, "%s (%s)\n", version, sourceRevision)
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	connectionFile := flags.String("connection-file", "", "mode-0600 JSON operator connection envelope")
	targetConnectionFile := flags.String("target-connection-file", "", "mode-0600 JSON directly authenticated target connection envelope")
	preparationRole := flags.String("preparation-role", "", "dormant rollback-preparation role")
	preparationConnectionFile := flags.String("preparation-connection-file", "", "mode-0600 directly authenticated V1 preparation connection envelope")
	capacityEvidenceFile := flags.String("capacity-evidence", "", "versioned capacity evidence JSON")
	backupEvidenceFile := flags.String("backup-evidence", "", "versioned backup and restore-test evidence JSON")
	restoreEvidenceFile := flags.String("restore-evidence", "", "versioned restored-database identity and artifact evidence JSON")
	cleanupEvidenceFile := flags.String("cleanup-evidence", "", "versioned cleanup backup/restore and fleet approval evidence JSON")
	cleanupSoak := flags.Duration("cleanup-soak", 0, "required accepted-layout soak duration before cleanup")
	fleetLedgerFile := flags.String("fleet-ledger", "", "versioned fleet transition ledger JSON")
	archiveManifestFile := flags.String("archive-manifest", "", "versioned immutable archive manifest JSON")
	archiveRoot := flags.String("archive-root", "", "retrieved archive root whose files must match the manifest")
	manifestPath := flags.String("manifest", "", "canonical application manifest")
	reportPath := flags.String("report", "", "atomic JSON report destination")
	confirm := flags.String("confirm", "", "state-bound confirmation token")
	migrationID := flags.String("migration-id", "", "migration identifier")
	validationDigest := flags.String("validation-digest", "", "validation digest")
	action := flags.String("action", "", "action to confirm")
	receiptType := flags.String("receipt-type", "", "durable receipt type; newest when omitted")
	attemptID := flags.String("attempt-id", "", "durable receipt attempt; newest when omitted")
	var writerProbeFiles stringList
	flags.Var(&writerProbeFiles, "writer-probe-file", "repeatable mode-0600 directly authenticated writer probe envelope")
	retirementConnectionFile := flags.String("retirement-connection-file", "", "mode-0600 cluster role-retirement coordinator connection envelope")
	unreleasedDisposable := flags.Bool("unreleased-disposable", false, "permit unreleased operations only in the identity-guarded disposable harness")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "validate-fleet" {
		if *fleetLedgerFile == "" {
			return errors.New("--fleet-ledger is required")
		}
		ledger, loadErr := loadFleetLedger(*fleetLedgerFile)
		if loadErr != nil {
			return loadErr
		}
		summary, validateErr := ledger.Validate()
		if validateErr != nil {
			return validateErr
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(summary)
	}
	if args[0] == "validate-archive" {
		if *archiveManifestFile == "" || *archiveRoot == "" {
			return errors.New("--archive-manifest and --archive-root are required")
		}
		manifest, loadErr := loadArchiveManifest(*archiveManifestFile)
		if loadErr != nil {
			return loadErr
		}
		verification, verifyErr := manifest.VerifyFiles(*archiveRoot)
		if verifyErr != nil {
			return verifyErr
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(verification)
	}
	connectionString, err := loadConnection(*connectionFile)
	if err != nil {
		return err
	}
	engine, err := migrate.New(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("opening operator connection: %w", err)
	}
	defer engine.Close()
	engine.AllowUnreleasedDisposable = *unreleasedDisposable
	engine.PreparationRole = *preparationRole
	if *targetConnectionFile != "" {
		engine.TargetConnectionString, err = loadConnection(*targetConnectionFile)
		if err != nil {
			return err
		}
	}
	if *preparationConnectionFile != "" {
		engine.PreparationConnectionString, err = loadConnection(*preparationConnectionFile)
		if err != nil {
			return err
		}
	}
	if *capacityEvidenceFile != "" {
		value, loadErr := loadCapacityEvidence(*capacityEvidenceFile)
		if loadErr != nil {
			return loadErr
		}
		engine.CapacityEvidence = &value
	}
	if *backupEvidenceFile != "" {
		value, loadErr := loadBackupEvidence(*backupEvidenceFile)
		if loadErr != nil {
			return loadErr
		}
		engine.BackupEvidence = &value
	}
	if *cleanupEvidenceFile != "" {
		value, loadErr := loadCleanupEvidence(*cleanupEvidenceFile)
		if loadErr != nil {
			return loadErr
		}
		engine.CleanupEvidence = &value
	}
	engine.RequiredCleanupSoak = *cleanupSoak
	for _, path := range writerProbeFiles {
		probe, loadErr := loadWriterProbe(path)
		if loadErr != nil {
			return loadErr
		}
		engine.WriterProbes = append(engine.WriterProbes, probe)
	}
	if *retirementConnectionFile != "" {
		engine.RetirementConnectionString, err = loadConnection(*retirementConnectionFile)
		if err != nil {
			return err
		}
	}
	var report migrate.Report
	switch args[0] {
	case "status", "health":
		report, err = engine.Status(ctx)
		report.Action = args[0]
	case "bootstrap":
		report, err = engine.Bootstrap(ctx)
	case "complete-bootstrap":
		if *migrationID == "" || *confirm == "" || engine.TargetConnectionString == "" || engine.RetirementConnectionString == "" {
			return errors.New("--migration-id, --confirm, --target-connection-file, and --retirement-connection-file are required")
		}
		report, err = engine.CompleteBootstrap(ctx, *migrationID, *confirm, engine.TargetConnectionString)
	case "preflight":
		var manifest migrate.Manifest
		manifest, err = loadManifest(*manifestPath)
		if err == nil {
			report, err = engine.Preflight(ctx, manifest)
		}
	case "migrate":
		var manifest migrate.Manifest
		manifest, err = loadManifest(*manifestPath)
		if err == nil {
			report, err = engine.Migrate(ctx, manifest, *confirm)
		}
	case "verify-fence":
		if *migrationID == "" {
			return errors.New("--migration-id is required")
		}
		report, err = engine.VerifyFence(ctx, *migrationID, *confirm)
	case "confirm":
		switch *action {
		case "migrate":
			var manifest migrate.Manifest
			manifest, err = loadManifest(*manifestPath)
			if err == nil {
				report, err = engine.ConfirmMigration(ctx, manifest)
			}
		case "accept", "smoke":
			if *migrationID == "" || *targetConnectionFile == "" {
				return errors.New("--migration-id and --target-connection-file are required")
			}
			var targetConnection string
			targetConnection, err = loadConnection(*targetConnectionFile)
			if err == nil {
				switch *action {
				case "accept":
					if *validationDigest == "" {
						return errors.New("--validation-digest is required for accept confirmation")
					}
					report, err = engine.ConfirmAcceptance(ctx, *migrationID, *validationDigest, targetConnection)
				case "smoke":
					report, err = engine.ConfirmSmoke(ctx, *migrationID, targetConnection)
				}
			}
		case "verify-fence", "resume", "rollback", "complete-v1-handoff":
			if *migrationID == "" {
				return errors.New("--migration-id is required")
			}
			report, err = engine.ConfirmOperation(ctx, *action, *migrationID)
		case "bootstrap-handoff":
			if *migrationID == "" || engine.TargetConnectionString == "" {
				return errors.New("--migration-id and --target-connection-file are required")
			}
			report, err = engine.ConfirmBootstrap(ctx, *migrationID, engine.TargetConnectionString)
		case "rebind-restore":
			if *restoreEvidenceFile == "" || engine.TargetConnectionString == "" {
				return errors.New("--restore-evidence and --target-connection-file are required")
			}
			var evidence migrate.RestoreEvidence
			evidence, err = loadRestoreEvidence(*restoreEvidenceFile)
			if err == nil {
				report, err = engine.ConfirmRebindRestore(ctx, evidence, engine.TargetConnectionString)
			}
		case "cleanup":
			if *migrationID == "" || engine.CleanupEvidence == nil || engine.RequiredCleanupSoak <= 0 {
				return errors.New("--migration-id, --cleanup-evidence, and a positive --cleanup-soak are required")
			}
			report, err = engine.ConfirmCleanup(ctx, *migrationID)
		default:
			return errors.New("--action must be bootstrap-handoff, migrate, verify-fence, resume, rollback, complete-v1-handoff, accept, smoke, rebind-restore, or cleanup")
		}
	case "resume":
		if *migrationID == "" {
			return errors.New("--migration-id is required")
		}
		report, err = engine.Resume(ctx, *migrationID, *confirm)
	case "validate":
		if *migrationID == "" {
			return errors.New("--migration-id is required")
		}
		report, err = engine.Validate(ctx, *migrationID)
	case "accept":
		if *migrationID == "" || *validationDigest == "" || *confirm == "" || *targetConnectionFile == "" || engine.RetirementConnectionString == "" {
			return errors.New("--migration-id, --validation-digest, --confirm, --target-connection-file, and --retirement-connection-file are required")
		}
		var targetConnection string
		targetConnection, err = loadConnection(*targetConnectionFile)
		if err == nil {
			report, err = engine.Accept(ctx, *migrationID, *validationDigest, *confirm, targetConnection)
		}
	case "smoke":
		if *migrationID == "" || *confirm == "" || *targetConnectionFile == "" {
			return errors.New("--migration-id, --confirm, and --target-connection-file are required")
		}
		var targetConnection string
		targetConnection, err = loadConnection(*targetConnectionFile)
		if err == nil {
			report, err = engine.Smoke(ctx, *migrationID, *confirm, targetConnection)
		}
	case "rollback":
		if *migrationID == "" {
			return errors.New("--migration-id is required")
		}
		report, err = engine.Rollback(ctx, *migrationID, *confirm)
	case "complete-v1-handoff":
		if *migrationID == "" || *confirm == "" || engine.PreparationConnectionString == "" {
			return errors.New("--migration-id, --confirm, and --preparation-connection-file are required")
		}
		report, err = engine.CompleteV1Handoff(ctx, *migrationID, *confirm)
	case "rebind-restore":
		if *restoreEvidenceFile == "" || *confirm == "" || engine.TargetConnectionString == "" || engine.RetirementConnectionString == "" {
			return errors.New("--restore-evidence, --confirm, --target-connection-file, and --retirement-connection-file are required")
		}
		var evidence migrate.RestoreEvidence
		evidence, err = loadRestoreEvidence(*restoreEvidenceFile)
		if err == nil {
			report, err = engine.RebindRestore(ctx, evidence, *confirm, engine.TargetConnectionString)
		}
	case "receipt":
		if *migrationID == "" {
			return errors.New("--migration-id is required")
		}
		report, err = engine.Receipt(ctx, *migrationID, *receiptType, *attemptID)
	case "cleanup":
		if *migrationID == "" || *confirm == "" || engine.CleanupEvidence == nil ||
			engine.RequiredCleanupSoak <= 0 || engine.RetirementConnectionString == "" {
			return errors.New("--migration-id, --confirm, --cleanup-evidence, a positive --cleanup-soak, and --retirement-connection-file are required")
		}
		report, err = engine.Cleanup(ctx, *migrationID, *confirm)
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
	if err != nil {
		return err
	}
	if *reportPath != "" {
		return migrate.WriteReport(*reportPath, report)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error {
	if value == "" {
		return errors.New("file path must not be empty")
	}
	*s = append(*s, value)
	return nil
}

func loadManifest(path string) (migrate.Manifest, error) {
	if path == "" {
		return migrate.Manifest{}, errors.New("--manifest is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return migrate.Manifest{}, err
	}
	defer file.Close()
	return migrate.DecodeManifest(file)
}

func loadConnection(path string) (string, error) {
	if path == "" {
		if value := os.Getenv("CONNECTION_STRING"); value != "" {
			return value, nil
		}
		return "", errors.New("--connection-file or CONNECTION_STRING is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("connection file %s must have mode 0600", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	envelope, err := migrate.DecodeConnectionEnvelope(file)
	if err != nil {
		return "", err
	}
	return envelope.ConnectionString, nil
}

func loadWriterProbe(path string) (migrate.WriterProbe, error) {
	info, err := os.Stat(path)
	if err != nil {
		return migrate.WriterProbe{}, err
	}
	if info.Mode().Perm() != 0o600 {
		return migrate.WriterProbe{}, fmt.Errorf("writer probe file %s must have mode 0600", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return migrate.WriterProbe{}, err
	}
	defer file.Close()
	return migrate.DecodeWriterProbe(file)
}

func loadCapacityEvidence(path string) (migrate.CapacityEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.CapacityEvidence{}, err
	}
	defer file.Close()
	return migrate.DecodeCapacityEvidence(file)
}

func loadBackupEvidence(path string) (migrate.BackupEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.BackupEvidence{}, err
	}
	defer file.Close()
	return migrate.DecodeBackupEvidence(file)
}

func loadRestoreEvidence(path string) (migrate.RestoreEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.RestoreEvidence{}, err
	}
	defer file.Close()
	return migrate.DecodeRestoreEvidence(file)
}

func loadCleanupEvidence(path string) (migrate.CleanupEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.CleanupEvidence{}, err
	}
	defer file.Close()
	return migrate.DecodeCleanupEvidence(file)
}

func loadFleetLedger(path string) (migrate.FleetLedger, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.FleetLedger{}, err
	}
	defer file.Close()
	return migrate.DecodeFleetLedger(file)
}

func loadArchiveManifest(path string) (migrate.ArchiveManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return migrate.ArchiveManifest{}, err
	}
	defer file.Close()
	return migrate.DecodeArchiveManifest(file)
}
