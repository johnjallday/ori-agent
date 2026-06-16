package userprofile

import (
	"fmt"
	"strings"
)

func RenderUserProfileSection(profile *UserProfile) string {
	if profile == nil || profile.IsEmpty() {
		return ""
	}

	lines := []string{
		"## About You",
		"",
		"This durable user profile is provided for personalization. Follow the current request first if it conflicts.",
	}

	add := func(label, value string) {
		if value = promptLine(value); value != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", label, value))
		}
	}

	add("Name", profile.DisplayName)
	add("Email", profile.Email)
	add("Timezone", profile.Timezone)
	add("Locale", profile.Locale)
	add("Role", profile.RoleCategory)
	if len(profile.Specializations) > 0 {
		parts := make([]string, 0, len(profile.Specializations))
		for _, item := range profile.Specializations {
			if value := promptLine(item); value != "" {
				parts = append(parts, value)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, "- Specializations: "+strings.Join(parts, ", "))
		}
	}
	for _, key := range preferenceRenderOrder {
		if value := promptLine(profile.Preferences[key]); value != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", preferenceLabel(key), value))
		}
	}
	add("About", profile.About)

	if len(lines) == 3 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (p *UserProfile) IsEmpty() bool {
	if p == nil {
		return true
	}
	return strings.TrimSpace(p.DisplayName) == "" &&
		strings.TrimSpace(p.Email) == "" &&
		strings.TrimSpace(p.Timezone) == "" &&
		strings.TrimSpace(p.Locale) == "" &&
		strings.TrimSpace(p.RoleCategory) == "" &&
		len(p.Specializations) == 0 &&
		len(p.Preferences) == 0 &&
		strings.TrimSpace(p.About) == ""
}

func promptLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func preferenceLabel(key string) string {
	switch key {
	case "response_style":
		return "Response style"
	case "units":
		return "Units"
	case "language":
		return "Language"
	default:
		return key
	}
}
