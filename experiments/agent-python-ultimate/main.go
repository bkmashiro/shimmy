package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const runReportSchema = "agent-python-ultimate-run-report/v1"

// sourceCommit is injected from a verified clean worktree with
// -ldflags "-X main.sourceCommit=<commit>".
var sourceCommit string

type RunMetadata struct {
	StartedUTC     string `json:"started_utc"`
	FinishedUTC    string `json:"finished_utc,omitempty"`
	Hostname       string `json:"hostname"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GoVersion      string `json:"go_version"`
	ExecutableSHA  string `json:"executable_sha256"`
	ArtifactSHA    string `json:"artifact_sha256"`
	ManifestSHA    string `json:"manifest_sha256"`
	ConfigSHA      string `json:"config_sha256"`
	SourceCommit   string `json:"source_commit"`
	SourceModified bool   `json:"source_modified"`
	BuildInfo      string `json:"build_info,omitempty"`
	SlurmJobID     string `json:"slurm_job_id,omitempty"`
	SlurmNode      string `json:"slurm_node,omitempty"`
	RowsPlanned    int    `json:"rows_planned"`
	RowsCompleted  int    `json:"rows_completed"`
	RowsResumed    int    `json:"rows_resumed"`
	Complete       bool   `json:"complete"`
	StopReason     string `json:"stop_reason,omitempty"`
	OutlierPolicy  string `json:"outlier_policy"`
	TimingClock    string `json:"timing_clock"`
	ObserverPolicy string `json:"observer_policy"`
}

type Aggregate struct {
	Key             string  `json:"key"`
	Rows            int     `json:"rows"`
	OK              int     `json:"ok"`
	Unavailable     int     `json:"unavailable"`
	Failed          int     `json:"failed"`
	StartupMedianNS int64   `json:"startup_median_ns,omitempty"`
	StartupP95NS    int64   `json:"startup_p95_ns,omitempty"`
	RequestMedianNS int64   `json:"request_median_ns,omitempty"`
	RequestP95NS    int64   `json:"request_p95_ns,omitempty"`
	MeanRSSBytes    float64 `json:"mean_rss_bytes,omitempty"`
	MeanPSSBytes    float64 `json:"mean_pss_bytes,omitempty"`
}

type RunReport struct {
	Schema     string         `json:"schema"`
	Metadata   RunMetadata    `json:"metadata"`
	Plan       Plan           `json:"plan"`
	Rows       []WorkerResult `json:"rows"`
	ByCampaign []Aggregate    `json:"by_campaign"`
	ByLane     []Aggregate    `json:"by_lane"`
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: %s plan|worker|run|validate ...", os.Args[0])
	}
	var err error
	switch os.Args[1] {
	case "plan":
		err = commandPlan(os.Args[2:])
	case "worker":
		err = commandWorker(os.Args[2:])
	case "run":
		err = commandRun(os.Args[2:])
	case "validate":
		err = commandValidate(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func commandPlan(args []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	configPath := flags.String("config", "configs/ultimate.json", "strict benchmark config")
	outputPath := flags.String("output", "-", "plan output or -")
	limit := flags.Int("limit", 0, "explicit local/smoke row limit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	plan, _, err := loadExpandedPlan(*configPath, *limit)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if *outputPath == "-" {
		_, err = os.Stdout.Write(append(raw, '\n'))
		return err
	}
	return atomicWrite(*outputPath, append(raw, '\n'), 0o600)
}

func commandWorker(args []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	inputPath := flags.String("input", "", "worker input JSON")
	outputPath := flags.String("output", "", "worker result JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *inputPath == "" || *outputPath == "" {
		return errors.New("worker requires --input and --output")
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		return err
	}
	input, err := ParseWorkerInput(raw)
	if err != nil {
		return err
	}
	result := RunWorker(context.Background(), input)
	return WriteWorkerResult(*outputPath, result)
}

func commandRun(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", "", "strict benchmark config")
	artifactPath := flags.String("artifact", "", "exact Agent Python wasm")
	manifestPath := flags.String("manifest", "", "exact artifact manifest")
	outputDir := flags.String("output", "", "private result directory")
	limit := flags.Int("limit", 0, "explicit smoke row limit")
	maxDuration := flags.Duration("max-duration", 0, "optional parent wall-time guard")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *artifactPath == "" || *manifestPath == "" || *outputDir == "" {
		return errors.New("run requires --config, --artifact, --manifest, and --output")
	}
	return runParent(*configPath, *artifactPath, *manifestPath, *outputDir, *limit, *maxDuration)
}

func commandValidate(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	dir := flags.String("output", "", "run result directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("validate requires --output")
	}
	report, err := rebuildReport(*dir)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(*dir, "report.recomputed.json"), report)
}

func runParent(configPath, artifactPath, manifestPath, outputDir string, limit int, maxDuration time.Duration) error {
	configPath, _ = filepath.Abs(configPath)
	artifactPath, _ = filepath.Abs(artifactPath)
	manifestPath, _ = filepath.Abs(manifestPath)
	outputDir, _ = filepath.Abs(outputDir)
	for _, path := range []string{configPath, artifactPath, manifestPath} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return fmt.Errorf("required input is not a non-empty regular file: %s", path)
		}
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "rows"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "logs"), 0o700); err != nil {
		return err
	}
	if free, err := freeBytes(outputDir); err != nil || free < 4<<30 {
		return fmt.Errorf("output filesystem requires at least 4 GiB free (free=%d, err=%v)", free, err)
	}

	plan, configRaw, err := loadExpandedPlan(configPath, limit)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outputDir, "plan.json"), plan); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	metadata := RunMetadata{
		StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Hostname: hostname,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		ExecutableSHA: fileSHA256(executable), ArtifactSHA: fileSHA256(artifactPath),
		ManifestSHA: fileSHA256(manifestPath), ConfigSHA: bytesSHA256(configRaw),
		SourceCommit: sourceCommit,
		SlurmJobID:   os.Getenv("SLURM_JOB_ID"), SlurmNode: os.Getenv("SLURMD_NODENAME"),
		RowsPlanned: len(plan.Rows), OutlierPolicy: "no deletion; warmups are separately labelled",
		TimingClock: "Go monotonic time embedded in time.Time", ObserverPolicy: "observer callback time excluded from each phase duration",
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		metadata.BuildInfo = info.String()
		buildRevision := ""
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				buildRevision = setting.Value
			case "vcs.modified":
				metadata.SourceModified = setting.Value == "true"
			}
		}
		if metadata.SourceCommit == "" {
			metadata.SourceCommit = buildRevision
		} else if buildRevision != "" && buildRevision != metadata.SourceCommit {
			return fmt.Errorf("linker source commit %s disagrees with build VCS revision %s", metadata.SourceCommit, buildRevision)
		}
	}
	if len(metadata.SourceCommit) != 40 {
		return errors.New("benchmark executable is not bound to a source commit")
	}
	if _, err := hex.DecodeString(metadata.SourceCommit); err != nil {
		return errors.New("benchmark executable source commit is not hexadecimal")
	}
	if metadata.SourceModified {
		return errors.New("benchmark executable was built from a modified source tree")
	}
	if err := writeJSON(filepath.Join(outputDir, "metadata.json"), metadata); err != nil {
		return err
	}

	deadline := time.Time{}
	deadlineReserve := 30 * time.Minute
	if maxDuration > 0 {
		deadline = time.Now().Add(maxDuration)
		if maxDuration < time.Hour {
			deadlineReserve = time.Minute
		}
	}
	if raw := os.Getenv("SLURM_JOB_END_TIME"); raw != "" {
		if seconds, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			slurmDeadline := time.Unix(seconds, 0)
			if deadline.IsZero() || slurmDeadline.Before(deadline) {
				deadline = slurmDeadline
			}
		}
	}

	cacheDir := filepath.Join(outputDir, "compile-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	if err := prewarmCompileCache(executable, artifactPath, manifestPath, cacheDir, outputDir, plan); err != nil {
		return fmt.Errorf("prewarm compile cache: %w", err)
	}

	for index, row := range plan.Rows {
		resultPath := filepath.Join(outputDir, "rows", row.ID+".json")
		if validWorkerResult(resultPath, row, "ok", "unavailable", "unsupported") {
			metadata.RowsResumed++
			metadata.RowsCompleted++
			continue
		}
		if !deadline.IsZero() && time.Until(deadline) < deadlineReserve {
			metadata.StopReason = fmt.Sprintf("deadline guard: less than %s remain", deadlineReserve)
			break
		}
		if free, err := freeBytes(outputDir); err != nil || free < 2<<30 {
			metadata.StopReason = fmt.Sprintf("disk guard: free=%d err=%v", free, err)
			break
		}
		input := WorkerInput{
			Schema: "agent-python-ultimate-worker-input/v1", Row: row,
			ArtifactPath: artifactPath, ManifestPath: manifestPath,
			CompileCacheDir: cacheDir, Seed: 20260727 + int64(index),
		}
		inputPath := filepath.Join(outputDir, "rows", row.ID+".input.json")
		if err := writeJSON(inputPath, input); err != nil {
			return err
		}
		stdoutPath := filepath.Join(outputDir, "logs", row.ID+".stdout")
		stderrPath := filepath.Join(outputDir, "logs", row.ID+".stderr")
		if err := runPlanWorkerProcess(executable, inputPath, resultPath, stdoutPath, stderrPath, row); err != nil {
			return fmt.Errorf("worker %s: %w", row.ID, err)
		}
		info, err := os.Stat(resultPath)
		if err != nil || info.Size() > 16<<20 {
			return fmt.Errorf("worker %s result size invalid: size=%d err=%v", row.ID, sizeOrZero(info), err)
		}
		if !validWorkerResult(resultPath, row, "ok", "unavailable", "unsupported", "failed") {
			return fmt.Errorf("worker %s did not produce a structurally valid exact-row result", row.ID)
		}
		metadata.RowsCompleted++
		if err := appendCheckpoint(filepath.Join(outputDir, "checkpoint.jsonl"), row.ID); err != nil {
			return err
		}
		metadata.FinishedUTC = time.Now().UTC().Format(time.RFC3339Nano)
		if err := writeJSON(filepath.Join(outputDir, "metadata.json"), metadata); err != nil {
			return err
		}
	}
	metadata.Complete = metadata.RowsCompleted == metadata.RowsPlanned
	metadata.FinishedUTC = time.Now().UTC().Format(time.RFC3339Nano)
	if !metadata.Complete && metadata.StopReason == "" {
		metadata.StopReason = "incomplete row set"
	}
	if err := writeJSON(filepath.Join(outputDir, "metadata.json"), metadata); err != nil {
		return err
	}
	report, err := rebuildReport(outputDir)
	if err != nil {
		return err
	}
	report.Metadata = metadata
	return writeJSON(filepath.Join(outputDir, "report.json"), report)
}

func prewarmCompileCache(executable, artifact, manifest, cacheDir, outputDir string, plan *Plan) error {
	marker := filepath.Join(cacheDir, ".prewarmed")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if len(plan.Rows) == 0 {
		return errors.New("cannot prewarm empty plan")
	}
	row := plan.Rows[0]
	row.ID = "internal-prewarm"
	row.Campaign = "capability"
	row.Lifecycle = LifecycleFresh
	row.Pool = 1
	row.PreparedCapacity = 1
	row.Repeat = 1
	row.InputBytes, row.OutputBytes, row.ArenaMiB, row.DirtyBps, row.Concurrency = nil, nil, nil, nil, nil
	row.CPUProfile, row.DirtyPattern, row.Fault = "none", "", ""
	row.SnapshotSelected, row.SnapshotFallback, row.Surface, row.CacheState = "", false, "direct", "warm"
	input := WorkerInput{Schema: "agent-python-ultimate-worker-input/v1", Row: row, ArtifactPath: artifact, ManifestPath: manifest, CompileCacheDir: cacheDir, Seed: 1}
	inputPath := filepath.Join(outputDir, "prewarm.input.json")
	resultPath := filepath.Join(outputDir, "prewarm.result.json")
	if err := writeJSON(inputPath, input); err != nil {
		return err
	}
	if err := runWorkerProcess(executable, inputPath, resultPath, filepath.Join(outputDir, "logs", "prewarm.stdout"), filepath.Join(outputDir, "logs", "prewarm.stderr")); err != nil {
		return err
	}
	if !validWorkerResult(resultPath, row, "ok") {
		return errors.New("prewarm worker did not produce a valid result")
	}
	return atomicWrite(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
}

func runPlanWorkerProcess(executable, inputPath, resultPath, stdoutPath, stderrPath string, row PlanRow) error {
	startedUTC := time.Now().UTC().Format(time.RFC3339Nano)
	if err := runWorkerProcess(executable, inputPath, resultPath, stdoutPath, stderrPath); err != nil {
		if !workerProcessCrashed(err, stderrPath) {
			return err
		}
		return writeJSON(resultPath, WorkerResult{
			Schema:      workerResultSchema,
			Row:         row,
			Status:      "failed",
			StartedUTC:  startedUTC,
			FinishedUTC: time.Now().UTC().Format(time.RFC3339Nano),
			Error:       fmt.Sprintf("worker process crashed: %v", err),
		})
	}
	return nil
}

func workerProcessCrashed(err error, stderrPath string) bool {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ProcessState == nil {
		return false
	}
	if exitError.ProcessState.ExitCode() == -1 {
		return true
	}
	if code := exitError.ExitCode(); code != 2 && code != 4 {
		return false
	}
	info, statErr := os.Stat(stderrPath)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return false
	}
	stderr, readErr := os.ReadFile(stderrPath)
	if readErr != nil {
		return false
	}
	return bytes.Contains(stderr, []byte("fatal error:")) && bytes.Contains(stderr, []byte("[signal SIG"))
}

func runWorkerProcess(executable, inputPath, resultPath, stdoutPath, stderrPath string) error {
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	command := exec.Command(executable, "worker", "--input", inputPath, "--output", resultPath)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}

func loadExpandedPlan(configPath string, limit int) (*Plan, []byte, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := ParsePlanConfig(raw)
	if err != nil {
		return nil, nil, err
	}
	plan, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: cfg.Seed})
	if err != nil {
		return nil, nil, err
	}
	if limit < 0 {
		return nil, nil, errors.New("limit must be non-negative")
	}
	if limit > 0 && limit < len(plan.Rows) {
		plan.Rows = append([]PlanRow(nil), plan.Rows[:limit]...)
	}
	return plan, raw, nil
}

func rebuildReport(outputDir string) (*RunReport, error) {
	planRaw, err := os.ReadFile(filepath.Join(outputDir, "plan.json"))
	if err != nil {
		return nil, err
	}
	plan, err := ParsePlanJSON(planRaw)
	if err != nil {
		return nil, err
	}
	metadata := RunMetadata{}
	if raw, readErr := os.ReadFile(filepath.Join(outputDir, "metadata.json")); readErr == nil {
		_ = json.Unmarshal(raw, &metadata)
	}
	rows := make([]WorkerResult, 0, len(plan.Rows))
	for _, row := range plan.Rows {
		path := filepath.Join(outputDir, "rows", row.ID+".json")
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var result WorkerResult
		if err := decodeStrictJSON(raw, &result); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if result.Schema != workerResultSchema || result.Row.ID != row.ID {
			return nil, fmt.Errorf("worker result identity mismatch: %s", path)
		}
		rows = append(rows, result)
	}
	return &RunReport{
		Schema: runReportSchema, Metadata: metadata, Plan: *plan, Rows: rows,
		ByCampaign: aggregateRows(rows, func(row WorkerResult) string { return row.Row.Campaign }),
		ByLane:     aggregateRows(rows, func(row WorkerResult) string { return string(row.Row.Lifecycle) }),
	}, nil
}

func aggregateRows(rows []WorkerResult, keyFn func(WorkerResult) string) []Aggregate {
	groups := map[string][]WorkerResult{}
	for _, row := range rows {
		groups[keyFn(row)] = append(groups[keyFn(row)], row)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Aggregate, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		var starts, requests []int64
		var rss, pss float64
		agg := Aggregate{Key: key, Rows: len(group)}
		for _, row := range group {
			switch row.Status {
			case "ok":
				agg.OK++
			case "unavailable", "unsupported":
				agg.Unavailable++
			default:
				agg.Failed++
			}
			starts = append(starts, int64(row.StartupDuration))
			for _, request := range row.Requests {
				requests = append(requests, int64(request.Duration))
			}
			rss += float64(row.BeforeShutdown.RSSBytes)
			pss += float64(row.BeforeShutdown.PSSBytes)
		}
		agg.StartupMedianNS, agg.StartupP95NS = percentile(starts, 0.5), percentile(starts, 0.95)
		agg.RequestMedianNS, agg.RequestP95NS = percentile(requests, 0.5), percentile(requests, 0.95)
		agg.MeanRSSBytes, agg.MeanPSSBytes = rss/float64(len(group)), pss/float64(len(group))
		out = append(out, agg)
	}
	return out
}

func percentile(values []int64, probability float64) int64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := int(math.Ceil(probability*float64(len(copyValues)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copyValues) {
		index = len(copyValues) - 1
	}
	return copyValues[index]
}

func validWorkerResult(path string, expectedRow PlanRow, allowedStatuses ...string) bool {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 16<<20 {
		return false
	}
	var result WorkerResult
	if decodeStrictJSON(raw, &result) != nil || result.Schema != workerResultSchema || !reflect.DeepEqual(result.Row, expectedRow) {
		return false
	}
	for _, status := range allowedStatuses {
		if result.Status == status {
			return true
		}
	}
	return false
}

func appendCheckpoint(path, rowID string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	entry, _ := json.Marshal(map[string]any{"row_id": rowID, "completed_utc": time.Now().UTC().Format(time.RFC3339Nano)})
	if _, err := file.Write(append(entry, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(raw, '\n'), 0o600)
}

func atomicWrite(path string, raw []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func freeBytes(path string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, err
	}
	return stats.Bavail * uint64(stats.Bsize), nil
}

func fileSHA256(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	_, _ = io.Copy(hash, file)
	return hex.EncodeToString(hash.Sum(nil))
}

func bytesSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sizeOrZero(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func fatalf(format string, values ...any) {
	message := fmt.Sprintf(format, values...)
	message = strings.TrimSpace(message)
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
