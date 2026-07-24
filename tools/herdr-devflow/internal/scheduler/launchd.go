package scheduler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

const LaunchAgentLabel = "com.ori.herdr-devflow"

// LaunchdConfig contains only stable user-local paths. In particular, it
// never points a detached dispatcher at a removable feature worktree.
type LaunchdConfig struct {
	GOOS        string
	HomeDir     string
	UID         int
	HelperPath  string
	RuntimeRoot string
	HerdrBinary string
	Launchctl   string
	Run         func(context.Context, string, ...string) error
}

// LaunchAgentPath returns the single user-level dispatcher plist location.
func LaunchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

// InstallLaunchAgent renders and registers one idempotent per-user launcher.
// The helper's dispatch command has no feature-worktree argument and reads
// only the persisted runtime root supplied here.
func InstallLaunchAgent(ctx context.Context, config LaunchdConfig) (string, error) {
	goos := config.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos != "darwin" {
		return "", &model.StageError{
			Stage:    "scheduler setup",
			Code:     model.ErrSchedulerUnsupported,
			Message:  "one-time continuation scheduling is supported only on macOS in this release",
			Recovery: "run scheduling commands on a macOS host; no cron, systemd, or Windows scheduler was installed",
		}
	}
	if config.HomeDir == "" || config.HelperPath == "" || config.RuntimeRoot == "" {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrScheduleInvalid, Message: "stable home, helper, and runtime paths are required", Recovery: "run wt herd setup from the Ori repository"}
	}
	if config.UID < 0 {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrScheduleInvalid, Message: "a non-negative macOS user id is required", Recovery: "run wt herd setup from your logged-in macOS account"}
	}
	if config.Launchctl == "" {
		config.Launchctl = "launchctl"
	}
	if config.Run == nil {
		config.Run = defaultLaunchctlRun
	}
	if err := os.MkdirAll(filepath.Join(config.HomeDir, "Library", "LaunchAgents"), 0700); err != nil {
		return "", stateSetupError("could not create the LaunchAgents directory", err)
	}
	if err := os.MkdirAll(filepath.Join(config.RuntimeRoot, "logs"), 0700); err != nil {
		return "", stateSetupError("could not create the scheduler log directory", err)
	}
	plistPath := LaunchAgentPath(config.HomeDir)
	contents, err := RenderLaunchAgent(config)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(plistPath, []byte(contents), 0600); err != nil {
		return "", stateSetupError("could not write the LaunchAgent plist", err)
	}
	domain := fmt.Sprintf("gui/%d", config.UID)
	job := domain + "/" + LaunchAgentLabel
	// Ignore bootout failure: it is expected on first registration. The scoped
	// job label makes this idempotent without touching unrelated launchd jobs.
	_ = config.Run(ctx, config.Launchctl, "bootout", job)
	if err := config.Run(ctx, config.Launchctl, "bootstrap", domain, plistPath); err != nil {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrSchedulerUnsupported, Message: "launchd could not register the continuation dispatcher", Recovery: "launchctl bootstrap " + domain + " " + plistPath, Cause: err}
	}
	if err := config.Run(ctx, config.Launchctl, "kickstart", "-k", job); err != nil {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrSchedulerUnsupported, Message: "launchd registered the dispatcher but could not start its first check", Recovery: "launchctl kickstart -k " + job, Cause: err}
	}
	return plistPath, nil
}

// RenderLaunchAgent produces XML without a shell command string so paths and
// user-provided binary locations cannot change argument boundaries.
func RenderLaunchAgent(config LaunchdConfig) (string, error) {
	if config.HelperPath == "" || config.RuntimeRoot == "" {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrScheduleInvalid, Message: "stable helper and runtime paths are required", Recovery: "run wt herd setup"}
	}
	arguments := []string{config.HelperPath, "--home", config.RuntimeRoot}
	if strings.TrimSpace(config.HerdrBinary) != "" {
		arguments = append(arguments, "--herdr-bin", config.HerdrBinary)
	}
	arguments = append(arguments, "dispatch")
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0"><dict>` + "\n")
	builder.WriteString("<key>Label</key><string>" + xmlEscape(LaunchAgentLabel) + "</string>\n")
	builder.WriteString("<key>ProgramArguments</key><array>\n")
	for _, argument := range arguments {
		builder.WriteString("<string>" + xmlEscape(argument) + "</string>\n")
	}
	builder.WriteString("</array>\n")
	builder.WriteString("<key>RunAtLoad</key><true/>\n")
	builder.WriteString("<key>StartInterval</key><integer>60</integer>\n")
	builder.WriteString("<key>ProcessType</key><string>Background</string>\n")
	builder.WriteString("<key>StandardOutPath</key><string>" + xmlEscape(filepath.Join(config.RuntimeRoot, "logs", "dispatch.out.log")) + "</string>\n")
	builder.WriteString("<key>StandardErrorPath</key><string>" + xmlEscape(filepath.Join(config.RuntimeRoot, "logs", "dispatch.err.log")) + "</string>\n")
	builder.WriteString("</dict></plist>\n")
	return builder.String(), nil
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func writeFileAtomic(path string, contents []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".launchagent-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func defaultLaunchctlRun(ctx context.Context, command string, args ...string) error {
	return exec.CommandContext(ctx, command, args...).Run()
}

func stateSetupError(message string, cause error) *model.StageError {
	return &model.StageError{Stage: "scheduler setup", Code: model.ErrStateCorrupt, Message: message, Recovery: "check the user-local Herdr Devflow runtime permissions, then run wt herd setup", Cause: cause}
}
