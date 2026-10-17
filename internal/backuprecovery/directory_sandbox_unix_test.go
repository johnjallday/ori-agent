//go:build darwin || linux

package backuprecovery

import (
	"strconv"
	"strings"
	"testing"
)

func TestBrokerPolicyRejectsOverlapsAndQuotesFixedNamespaces(t *testing.T) {
	for _, repository := range []string{"", "relative", "/", "/source", "/source/nested", "/invented", "/invented/helper"} {
		if profile, err := metadataBrokerSandboxProfile("/invented/helper", []string{"/source"}, repository); profile != "" || err != errSandboxProfile {
			t.Fatal("unsafe broker profile accepted")
		}
	}
	repository := `/invented repository " (allow default)`
	profile, err := metadataBrokerSandboxProfile("/invented/helper", []string{"/source"}, repository)
	if err != nil || !strings.Contains(profile, "(literal "+strconv.Quote(repository+"/config")+")") || !strings.Contains(profile, "(require-not (subpath "+strconv.Quote(repository+"/keys")+"))") {
		t.Fatal("repository namespaces were not literal/bounded")
	}
}

func TestDirectoryMetadataPolicyQuotesPathsAndDeniesSourceData(t *testing.T) {
	executable := "/invented/helper"
	source := `/invented/source " (allow default) \ name`
	profile, err := directoryMetadataSandboxProfile(executable, []string{source})
	if err != nil || !strings.Contains(profile, "(deny file-read-data (subpath "+strconv.Quote(source)+"))") || !strings.Contains(profile, "(deny network*)") || !strings.Contains(profile, "(deny default)") {
		t.Fatal("policy did not constrain/quote the exact source")
	}
	for _, sources := range [][]string{nil, {"/"}, {"relative"}, {"/a/../b"}, {"/source\n"}, {"/invented"}, {executable}, {"/a", "/a"}, make([]string, maxSelectedSources+1)} {
		if profile, err := directoryMetadataSandboxProfile(executable, sources); profile != "" || err != errSandboxProfile {
			t.Fatal("unsafe policy input accepted")
		}
	}
	for _, path := range []string{"", "restic", "/", "/helper\x00"} {
		if _, err := directoryMetadataSandboxProfile(path, []string{"/source"}); err != errSandboxProfile {
			t.Fatal("unsafe executable profile accepted")
		}
	}
}
