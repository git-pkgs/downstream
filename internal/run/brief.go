package run

import (
	"sync"

	"github.com/git-pkgs/brief"
	"github.com/git-pkgs/brief/detect"
	"github.com/git-pkgs/brief/kb"
)

var loadKB = sync.OnceValues(func() (*kb.KnowledgeBase, error) {
	return kb.Load(brief.KnowledgeFS)
})

// briefDetect runs brief's detection engine against dir and returns
// the project's test command and whether it came from a project
// script (Makefile target, package.json script, etc.) as opposed to a
// generic per-language default. A project-script command should be
// used as-is; a generic default can be replaced by auto-narrowing.
//
// Linked as a library rather than shelling out so downstream doesn't
// need brief on PATH and so brief's knowledge base is the single
// source of per-ecosystem test commands.
func briefDetect(dir string) (cmd string, fromProject bool) {
	knowledge, err := loadKB()
	if err != nil {
		return "", false
	}
	report, err := detect.New(knowledge, dir).Run()
	if err != nil {
		return "", false
	}
	return extractBriefTest(report)
}

func extractBriefTest(r *brief.Report) (cmd string, fromProject bool) {
	for _, t := range r.Tools["test"] {
		if t.Command == nil || t.Command.Run == "" {
			continue
		}
		// Only knowledge_base is a generic per-language default that
		// it's safe to replace with auto-narrowing; project_script
		// and config_file both reflect a choice made in the repo.
		return t.Command.Run, t.Command.Source != brief.SourceKnowledgeBase
	}
	for _, s := range r.Scripts {
		if s.Name == "test" && s.Run != "" {
			return s.Run, true
		}
	}
	return "", false
}
