package run

import (
	"context"
	"encoding/json"
	"os/exec"
)

// briefDetect runs the brief CLI against dir and returns the
// project's test command and whether it came from a project script
// (Makefile target, package.json script, etc.) as opposed to a
// generic per-language default. A project-script command should be
// used as-is; a generic default can be replaced by auto-narrowing.
//
// Returns ("", false) if brief is not installed or finds nothing.
func briefDetect(ctx context.Context, dir string) (cmd string, fromProject bool) {
	out, err := exec.CommandContext(ctx, "brief", "-json", dir).Output()
	if err != nil {
		return "", false
	}

	var b briefOutput
	if err := json.Unmarshal(out, &b); err != nil {
		return "", false
	}
	return extractBriefTest(b)
}

func extractBriefTest(b briefOutput) (cmd string, fromProject bool) {
	for _, t := range b.Tools.Test {
		if t.Command.Run == "" {
			continue
		}
		// brief defines project_script, knowledge_base, config_file;
		// only knowledge_base is a generic per-language default that
		// it's safe to replace with auto-narrowing.
		return t.Command.Run, t.Command.Source != "knowledge_base"
	}
	for _, s := range b.Scripts {
		if s.Name == "test" && s.Run != "" {
			return s.Run, true
		}
	}
	return "", false
}

type briefOutput struct {
	Tools struct {
		Test []briefTool `json:"test"`
	} `json:"tools"`
	Scripts []briefScript `json:"scripts"`
}

type briefTool struct {
	Name    string `json:"name"`
	Command struct {
		Run    string `json:"run"`
		Source string `json:"source"`
	} `json:"command"`
}

type briefScript struct {
	Name string `json:"name"`
	Run  string `json:"run"`
}
