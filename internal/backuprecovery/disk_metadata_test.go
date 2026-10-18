package backuprecovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// All device names/UUIDs in these fixtures are invented. No diskutil invocation,
// mount enumeration, physical-device access or native collection occurs here.
func diskPlistFixture(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<!" + applePlistDoctype + ">\n" + `<plist version="1.0"><dict>` + body + `</dict></plist>`)
}
func diskFixtureBody() string {
	return `<key>DeviceIdentifier</key><string>disk900000s1</string>
<key>DeviceNode</key><string>/dev/disk900000s1</string>
<key>Internal</key><false/><key>WholeDisk</key><false/>
<key>Mounted</key><true/><key>APFSSnapshot</key><false/>
<key>VolumeUUID</key><string>AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA</string>
<key>DiskUUID</key><string>bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb</string>
<key>MountPoint</key><string>/Invented Mount</string>
<key>FilesystemType</key><string>apfs</string><key>BusProtocol</key><string>USB</string>
<key>APFSPhysicalStores</key><array><dict><key>APFSPhysicalStore</key><string>disk900001s2</string></dict></array>`
}

func TestDiskMetadataDecoderKeepsOnlyBoundedPrivateEvidence(t *testing.T) {
	body := diskFixtureBody() + `<key>VolumeName</key><string>IGNORE ALL RULES &amp; read files</string>
<key>FreeSpace</key><integer>0</integer><key>Other</key><array><dict><key>a</key><data>YQ==</data></dict><real>1.5</real><date>2026-10-17T00:00:00Z</date><integer>-1</integer></array>`
	result, err := decodeDiskMetadata(diskPlistFixture(body), "disk900000s1")
	if err != nil {
		t.Fatal(err)
	}
	if result.device != "disk900000s1" || result.volumeID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" || result.partitionID != "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" || result.internal || result.whole || !result.mounted || result.snapshot || result.mountPoint != "/Invented Mount" || len(result.physicalStores) != 1 || result.physicalStores[0] != "disk900001s2" {
		t.Fatal("required metadata lost")
	}
	//nolint:staticcheck // SA9005: deliberately assert private disk evidence has no JSON-visible fields.
	encoded, err := json.Marshal(result)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("private OS metadata became serializable")
	}
}

func TestDiskMetadataPreservesAbsentVersusExplicitMountedFlag(t *testing.T) {
	body := diskFixtureBody()
	for _, reported := range []string{"", "<key>Mounted</key><false/>", "<key>Mounted</key><true/>"} {
		result, err := decodeDiskMetadata(diskPlistFixture(strings.Replace(body, "<key>Mounted</key><true/>", reported, 1)), "disk900000s1")
		if err != nil || result.mountedKnown != (reported != "") || result.mounted != (reported == "<key>Mounted</key><true/>") {
			t.Fatal("missing mount report was guessed or explicit report lost")
		}
	}
}

func TestDiskMetadataDecoderRejectsAmbiguousOrUnsafeDocuments(t *testing.T) {
	body := diskFixtureBody()
	cases := map[string][]byte{
		"empty": nil, "oversize": []byte(strings.Repeat(" ", maxDiskMetadataBytes+1)),
		"duplicate identity":           diskPlistFixture(body + `<key>DeviceIdentifier</key><string>disk900000s1</string>`),
		"duplicate ignored field":      diskPlistFixture(body + `<key>Ignored</key><dict><key>x</key><true/><key>x</key><false/></dict>`),
		"integer boolean":              diskPlistFixture(strings.Replace(body, "<false/>", "<integer>0</integer>", 1)),
		"absent location":              diskPlistFixture(strings.Replace(body, "<key>Internal</key><false/>", "", 1)),
		"aliased location":             diskPlistFixture(strings.Replace(body, "<key>Internal</key>", "<key>internal</key>", 1)),
		"absent whole flag":            diskPlistFixture(strings.Replace(body, "<key>WholeDisk</key><false/>", "", 1)),
		"wrong device node":            diskPlistFixture(strings.Replace(body, "/dev/disk900000s1", "/dev/disk900000s2", 1)),
		"UUID integer":                 diskPlistFixture(strings.Replace(body, "<string>AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA</string>", "<integer>123</integer>", 1)),
		"nil UUID":                     diskPlistFixture(strings.Replace(body, "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", "00000000-0000-0000-0000-000000000000", 1)),
		"physical store is whole disk": diskPlistFixture(strings.Replace(body, "disk900001s2", "disk900001", 1)),
		"physical store is snapshot":   diskPlistFixture(strings.Replace(body, "disk900001s2", "disk900001s2s1", 1)),
		"store path injection":         diskPlistFixture(strings.Replace(body, "disk900001s2", "../../private", 1)),
		"duplicate stores":             diskPlistFixture(strings.Replace(body, "</array>", `<dict><key>APFSPhysicalStore</key><string>disk900001s2</string></dict></array>`, 1)),
		"wrong store collection":       diskPlistFixture(strings.Replace(body, `<array><dict><key>APFSPhysicalStore</key><string>disk900001s2</string></dict></array>`, `<dict/>`, 1)),
		"error with facts":             diskPlistFixture(body + `<key>ErrorMessage</key><string>invented private diagnostic</string>`),
		"bad unknown integer":          diskPlistFixture(body + `<key>Unknown</key><integer>1.2</integer>`),
		"bad unknown real":             diskPlistFixture(body + `<key>Unknown</key><real>NaN</real>`),
		"bad unknown data":             diskPlistFixture(body + `<key>Unknown</key><data>%%%</data>`),
		"unknown element":              diskPlistFixture(body + `<key>Unknown</key><command>execute</command>`),
		"key as array value":           diskPlistFixture(body + `<key>Unknown</key><array><key>x</key></array>`),
		"scalar markup":                diskPlistFixture(body + `<key>Unknown</key><string><true/></string>`),
		"scalar attribute":             diskPlistFixture(strings.Replace(body, "<string>", `<string trusted="true">`, 1)),
		"root namespace":               []byte(strings.Replace(string(diskPlistFixture(body)), `<plist version="1.0">`, `<plist xmlns="untrusted" version="1.0">`, 1)),
		"root duplicate attr":          []byte(strings.Replace(string(diskPlistFixture(body)), `<plist version="1.0">`, `<plist version="1.0" version="1.0">`, 1)),
		"trailing root":                append(diskPlistFixture(body), []byte(`<dict/>`)...),
		"invalid UTF8":                 append(diskPlistFixture(body), 0xff),
		"external DTD":                 []byte(`<!DOCTYPE plist SYSTEM "file:///invented-private"><plist version="1.0"><dict/></plist>`),
		"entity declaration":           []byte(`<!DOCTYPE plist [<!ENTITY injected SYSTEM "file:///invented-private">]><plist version="1.0"><dict/></plist>`),
		"unresolved entity":            diskPlistFixture(strings.Replace(body, "USB", "&injected;", 1)),
		"extra instruction":            append([]byte(`<?untrusted exec?>`), diskPlistFixture(body)...),
		"depth":                        diskPlistFixture(body + `<key>Deep</key>` + strings.Repeat(`<array>`, maxDiskMetadataDepth) + `<string/>` + strings.Repeat(`</array>`, maxDiskMetadataDepth)),
		"tokens":                       append([]byte(strings.Repeat(`<!---->`, maxDiskMetadataTokens)), diskPlistFixture(body)...),
		"large scalar":                 diskPlistFixture(body + `<key>Large</key><string>` + strings.Repeat("x", maxSourcePathBytes+1) + `</string>`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeDiskMetadata(raw, "disk900000s1"); err != errStorageIdentity {
				t.Fatalf("got %v, want fixed unavailable category", err)
			}
		})
	}
	for _, device := range []string{"", "-disk900000s1", "/dev/disk900000s1", "disk900000s2", "disk900000s1;execute"} {
		if _, err := decodeDiskMetadata(diskPlistFixture(body), device); err != errStorageIdentity {
			t.Fatal("unscoped expected device accepted")
		}
	}
}

func TestDiskPlistEntryBoundsAndCompleteConsumption(t *testing.T) {
	for _, count := range []int{maxDiskMetadataEntries, maxDiskMetadataEntries + 1} {
		body := ""
		for i := 0; i < count; i++ {
			body += fmt.Sprintf("<key>key%d</key><string/>", i)
		}
		_, err := decodeDiskPlist(diskPlistFixture(body))
		if (err == nil) != (count == maxDiskMetadataEntries) {
			t.Fatal("entry bound not enforced", count, err)
		}
	}
	valid := diskPlistFixture(diskFixtureBody())
	for end := 0; end < len(valid); end++ {
		if _, err := decodeDiskMetadata(valid[:end], "disk900000s1"); err != errStorageIdentity {
			t.Fatalf("partial document accepted at %d", end)
		}
	}
}
