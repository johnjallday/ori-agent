package workspace

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The Home profile is the Home's small structured record of where its owner
// works: which applications are installed, which one is the main one, which
// templates exist and what a new project should default to. The Home's package
// declares which rows the card shows and under which words; this record holds
// the values. It lives on AssistantProgramState so it travels in the Home
// envelope with the workspace root, never in the global user profile.
//
// Every value says where it came from. A detected value is a hint; a value the
// owner confirmed or set is the owner's instruction. The two are never merged.

const (
	HomeProfileSchemaVersion = 1

	// HomeProfileSourceDetected is a value Ori found. It stays a hint until
	// ConfirmedAt is set.
	HomeProfileSourceDetected = "detected"
	// HomeProfileSourceOwner is a value the owner set on the card.
	HomeProfileSourceOwner = "owner"

	// HomeProfileMainAppOnly and HomeProfileMainAppLibrary say why a detected
	// main application was proposed: it is the only one found, or most of the
	// Home's library entries are its projects.
	HomeProfileMainAppOnly    = "only_app"
	HomeProfileMainAppLibrary = "library_majority"

	// HomeProfileTemplatesFolderOffer is the folder card's Set up, which may
	// grant the consent only on the run that creates the Home.
	// HomeProfileTemplatesHomeReview is the Review dialog on the Home card.
	HomeProfileTemplatesFolderOffer = "folder_offer"
	HomeProfileTemplatesHomeReview  = "home_review"

	HomeProfileTemplateProject = "project"
	HomeProfileTemplateTrack   = "track"

	// HomeProfileTemplatesUnavailable and HomeProfileTemplatesFailed are what a
	// templates read that listed nothing left behind, so the card can say what
	// happened: the installed project plugin has no facts operation, or the
	// operation failed.
	HomeProfileTemplatesUnavailable = "operation_unavailable"
	HomeProfileTemplatesFailed      = "read_failed"

	HomeProfileMaxApps      = 16
	HomeProfileMaxTemplates = 64
	HomeProfileMaxRequests  = 16

	HomeProfileMinTempo = 40
	HomeProfileMaxTempo = 240
)

// HomeProfileSampleRates and HomeProfileBitDepths are the only values a
// new-project default may take.
var (
	HomeProfileSampleRates = []int{44100, 48000, 88200, 96000, 176400, 192000}
	HomeProfileBitDepths   = []int{16, 24, 32}
)

// ErrHomeProfileInvalid means a profile failed validation. A stored one that
// fails is treated as no profile.
var ErrHomeProfileInvalid = errors.New("home profile is invalid")

var homeProfileIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// homeProfilePluginIDPattern accepts every id a package may be installed
// under, which unlike an application id or an action may contain dots.
var homeProfilePluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// HomeProfile is the stored record.
type HomeProfile struct {
	SchemaVersion int `json:"schema_version"`
	// Revision increases on every write. A write that names an older one is
	// refused, so two open cards cannot overwrite each other.
	Revision   int64                 `json:"revision"`
	DeclaredBy HomeProfileDeclaredBy `json:"declared_by"`
	// DetectedAt is the last time installed applications were looked for. Nil
	// means never: the card offers Detect instead of saying none was found.
	DetectedAt *time.Time            `json:"detected_at,omitempty"`
	Apps       []HomeProfileApp      `json:"apps,omitempty"`
	MainApp    *HomeProfileMainApp   `json:"main_app,omitempty"`
	Templates  *HomeProfileTemplates `json:"templates,omitempty"`
	Defaults   *HomeProfileDefaults  `json:"defaults,omitempty"`
	// Requests are bounded idempotency receipts: a repeated request_id replays
	// instead of writing twice.
	Requests []HomeProfileRequest `json:"requests,omitempty"`
}

// The kinds of row a profile card can have. The package chooses which to show
// and what to call them; the host owns what each one stores.
const (
	HomeProfileKindApps      = "apps"
	HomeProfileKindMainApp   = "main_app"
	HomeProfileKindTemplates = "templates"
	HomeProfileKindDefaults  = "defaults"
)

// HomeProfileDeclaredBy names the package release whose declaration the record
// was last written under, with the title and row labels it gave the card, so a
// reader that has only the Home (an agent's context block) can use the
// package's words instead of the host's.
type HomeProfileDeclaredBy struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`
	Title    string `json:"title,omitempty"`
	// Labels maps a row kind to its declared label.
	Labels map[string]string `json:"labels,omitempty"`
}

// Label returns the declared label of a row kind, or fallback when the record
// was written without one.
func (d HomeProfileDeclaredBy) Label(kind, fallback string) string {
	if label := strings.TrimSpace(d.Labels[kind]); label != "" {
		return label
	}
	return fallback
}

// HomeProfileApp is one application found on this computer. ID and Name come
// from the host's tool table. Hidden is the owner's "Not mine": the row is
// kept so a later detection does not bring it back, and agents never see it.
type HomeProfileApp struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Detected    bool       `json:"detected"`
	DetectedAt  *time.Time `json:"detected_at,omitempty"`
	Version     string     `json:"version,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	Hidden      bool       `json:"hidden,omitempty"`
}

// HomeProfileMainApp is the application the owner mainly works in.
type HomeProfileMainApp struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	// Reason is set only on a detected value: why Ori proposed it.
	Reason      string     `json:"reason,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// HomeProfileTemplatesConsent is the owner's agreement that Ori may list one
// application's templates folders. RevokedAt is Forget.
type HomeProfileTemplatesConsent struct {
	GrantedAt time.Time  `json:"granted_at"`
	Source    string     `json:"source"`
	OfferID   string     `json:"offer_id,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// HomeProfileTemplate is one listed template: a display name, its kind and its
// file name inside the application's templates folder. Never a path.
type HomeProfileTemplate struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	File       string     `json:"file"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
}

// HomeProfileTemplates is the templates row. Items exist only under an active
// consent; Forget clears them and revokes it in one write.
type HomeProfileTemplates struct {
	Consent *HomeProfileTemplatesConsent `json:"consent,omitempty"`
	ReadAt  *time.Time                   `json:"read_at,omitempty"`
	AppID   string                       `json:"app_id,omitempty"`
	Items   []HomeProfileTemplate        `json:"items,omitempty"`
	// Truncated means the folders hold more than HomeProfileMaxTemplates.
	Truncated bool `json:"truncated,omitempty"`
	// Problem and ProblemAt record a read that listed nothing.
	Problem   string     `json:"problem,omitempty"`
	ProblemAt *time.Time `json:"problem_at,omitempty"`
}

// HomeProfileDefaults are what a new project starts from. A zero or empty
// part is not set.
type HomeProfileDefaults struct {
	TempoBPM      int        `json:"tempo_bpm,omitempty"`
	TimeSignature string     `json:"time_signature,omitempty"`
	SampleRateHz  int        `json:"sample_rate_hz,omitempty"`
	BitDepth      int        `json:"bit_depth,omitempty"`
	Source        string     `json:"source"`
	ConfirmedAt   *time.Time `json:"confirmed_at,omitempty"`
}

// HomeProfileRequest is one handled request_id.
type HomeProfileRequest struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`
	Revision   int64     `json:"revision"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Clone returns a deep copy.
func (p *HomeProfile) Clone() *HomeProfile {
	if p == nil {
		return nil
	}
	clone := *p
	if p.DeclaredBy.Labels != nil {
		clone.DeclaredBy.Labels = make(map[string]string, len(p.DeclaredBy.Labels))
		for kind, label := range p.DeclaredBy.Labels {
			clone.DeclaredBy.Labels[kind] = label
		}
	}
	clone.DetectedAt = cloneTime(p.DetectedAt)
	clone.Apps = make([]HomeProfileApp, len(p.Apps))
	for i, app := range p.Apps {
		app.DetectedAt, app.ConfirmedAt = cloneTime(app.DetectedAt), cloneTime(app.ConfirmedAt)
		clone.Apps[i] = app
	}
	if len(clone.Apps) == 0 {
		clone.Apps = nil
	}
	if p.MainApp != nil {
		main := *p.MainApp
		main.ConfirmedAt = cloneTime(p.MainApp.ConfirmedAt)
		clone.MainApp = &main
	}
	clone.Templates = p.Templates.Clone()
	if p.Defaults != nil {
		defaults := *p.Defaults
		defaults.ConfirmedAt = cloneTime(p.Defaults.ConfirmedAt)
		clone.Defaults = &defaults
	}
	clone.Requests = append([]HomeProfileRequest(nil), p.Requests...)
	return &clone
}

// Clone returns a deep copy.
func (t *HomeProfileTemplates) Clone() *HomeProfileTemplates {
	if t == nil {
		return nil
	}
	clone := *t
	if t.Consent != nil {
		consent := *t.Consent
		consent.RevokedAt = cloneTime(t.Consent.RevokedAt)
		clone.Consent = &consent
	}
	clone.ReadAt, clone.ProblemAt = cloneTime(t.ReadAt), cloneTime(t.ProblemAt)
	clone.Items = make([]HomeProfileTemplate, len(t.Items))
	for i, item := range t.Items {
		item.ModifiedAt = cloneTime(item.ModifiedAt)
		clone.Items[i] = item
	}
	if len(clone.Items) == 0 {
		clone.Items = nil
	}
	return &clone
}

// Active is true for a well-formed consent that has not been revoked.
func (c *HomeProfileTemplatesConsent) Active() bool {
	return c != nil && c.RevokedAt == nil && c.validate() == nil
}

func (c *HomeProfileTemplatesConsent) validate() error {
	switch {
	case c == nil:
		return invalidHomeProfile("templates consent is missing")
	case c.GrantedAt.IsZero():
		return invalidHomeProfile("templates consent has no granted_at")
	case c.Source != HomeProfileTemplatesFolderOffer && c.Source != HomeProfileTemplatesHomeReview:
		return invalidHomeProfile("templates consent source is unknown")
	case !consentText(c.OfferID, 160, true):
		return invalidHomeProfile("templates consent offer_id is invalid")
	case c.RevokedAt != nil && c.RevokedAt.Before(c.GrantedAt):
		return invalidHomeProfile("templates consent was revoked before it was granted")
	}
	return nil
}

// App returns the row with this id, hidden or not.
func (p *HomeProfile) App(id string) (HomeProfileApp, bool) {
	if p == nil {
		return HomeProfileApp{}, false
	}
	for _, app := range p.Apps {
		if app.ID == id {
			return app, true
		}
	}
	return HomeProfileApp{}, false
}

// VisibleApps returns the rows the owner has not hidden, in stored order.
func (p *HomeProfile) VisibleApps() []HomeProfileApp {
	if p == nil {
		return nil
	}
	var visible []HomeProfileApp
	for _, app := range p.Apps {
		if !app.Hidden {
			visible = append(visible, app)
		}
	}
	return visible
}

// Request returns the receipt of a handled request_id.
func (p *HomeProfile) Request(id string) (HomeProfileRequest, bool) {
	if p == nil || id == "" {
		return HomeProfileRequest{}, false
	}
	for _, request := range p.Requests {
		if request.ID == id {
			return request, true
		}
	}
	return HomeProfileRequest{}, false
}

// Empty is true for defaults with no part set.
func (d *HomeProfileDefaults) Empty() bool {
	return d == nil || (d.TempoBPM == 0 && d.TimeSignature == "" && d.SampleRateHz == 0 && d.BitDepth == 0)
}

func invalidHomeProfile(what string) error {
	return fmt.Errorf("%w: %s", ErrHomeProfileInvalid, what)
}

// homeProfileLine accepts one trimmed line of plain text of at most limit
// characters. Empty is accepted only when optional.
func homeProfileLine(value string, limit int, optional bool) bool {
	if value == "" {
		return optional
	}
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

// HomeProfileTemplateFileValid accepts a bare file name: no directory, no
// parent reference, nothing absolute.
func HomeProfileTemplateFileValid(file string) bool {
	return homeProfileLine(file, 255, false) && file != "." && file != ".." &&
		!strings.ContainsAny(file, `/\:`)
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Validate keeps a profile bounded and internally consistent.
func (p *HomeProfile) Validate() error {
	if p == nil {
		return invalidHomeProfile("missing")
	}
	if p.SchemaVersion != HomeProfileSchemaVersion || p.Revision < 1 {
		return invalidHomeProfile("schema_version or revision")
	}
	if !homeProfilePluginIDPattern.MatchString(p.DeclaredBy.PluginID) || !homeProfileLine(p.DeclaredBy.Version, 64, false) ||
		!homeProfileLine(p.DeclaredBy.Title, 60, true) || len(p.DeclaredBy.Labels) > 4 {
		return invalidHomeProfile("declared_by")
	}
	for kind, label := range p.DeclaredBy.Labels {
		switch kind {
		case HomeProfileKindApps, HomeProfileKindMainApp, HomeProfileKindTemplates, HomeProfileKindDefaults:
		default:
			return invalidHomeProfile("declared_by label kind")
		}
		if !homeProfileLine(label, 40, false) {
			return invalidHomeProfile("declared_by label")
		}
	}
	if len(p.Apps) > HomeProfileMaxApps {
		return invalidHomeProfile("too many apps")
	}
	seen := make(map[string]HomeProfileApp, len(p.Apps))
	for _, app := range p.Apps {
		if !homeProfileIDPattern.MatchString(app.ID) || !homeProfileLine(app.Name, 80, false) || !homeProfileLine(app.Version, 40, true) {
			return invalidHomeProfile("app identity")
		}
		if _, duplicate := seen[app.ID]; duplicate {
			return invalidHomeProfile("app is listed twice")
		}
		if app.Detected && (app.DetectedAt == nil || app.DetectedAt.IsZero()) {
			return invalidHomeProfile("detected app has no detected_at")
		}
		if app.Hidden && app.ConfirmedAt != nil {
			return invalidHomeProfile("hidden app is confirmed")
		}
		seen[app.ID] = app
	}
	if main := p.MainApp; main != nil {
		app, listed := seen[main.ID]
		if !listed || app.Hidden {
			return invalidHomeProfile("main_app is not a listed app")
		}
		switch main.Source {
		case HomeProfileSourceDetected:
			if main.Reason != HomeProfileMainAppOnly && main.Reason != HomeProfileMainAppLibrary {
				return invalidHomeProfile("main_app reason")
			}
		case HomeProfileSourceOwner:
			if main.ConfirmedAt == nil || main.Reason != "" {
				return invalidHomeProfile("owner main_app is not confirmed")
			}
		default:
			return invalidHomeProfile("main_app source")
		}
	}
	if err := p.Templates.validate(seen); err != nil {
		return err
	}
	if defaults := p.Defaults; defaults != nil {
		if defaults.Empty() || defaults.Source != HomeProfileSourceOwner || defaults.ConfirmedAt == nil {
			return invalidHomeProfile("defaults are empty or not the owner's")
		}
		if defaults.TempoBPM != 0 && (defaults.TempoBPM < HomeProfileMinTempo || defaults.TempoBPM > HomeProfileMaxTempo) {
			return invalidHomeProfile("tempo_bpm")
		}
		if !homeProfileLine(defaults.TimeSignature, 16, true) {
			return invalidHomeProfile("time_signature")
		}
		if defaults.SampleRateHz != 0 && !containsInt(HomeProfileSampleRates, defaults.SampleRateHz) {
			return invalidHomeProfile("sample_rate_hz")
		}
		if defaults.BitDepth != 0 && !containsInt(HomeProfileBitDepths, defaults.BitDepth) {
			return invalidHomeProfile("bit_depth")
		}
	}
	if len(p.Requests) > HomeProfileMaxRequests {
		return invalidHomeProfile("too many request receipts")
	}
	for _, request := range p.Requests {
		if !consentText(request.ID, 120, false) || !homeProfileIDPattern.MatchString(request.Action) ||
			request.Revision < 1 || request.Revision > p.Revision || request.RecordedAt.IsZero() {
			return invalidHomeProfile("request receipt")
		}
	}
	return nil
}

func (t *HomeProfileTemplates) validate(apps map[string]HomeProfileApp) error {
	if t == nil {
		return nil
	}
	if t.Consent != nil {
		if err := t.Consent.validate(); err != nil {
			return err
		}
	}
	if len(t.Items) > HomeProfileMaxTemplates {
		return invalidHomeProfile("too many templates")
	}
	listed := len(t.Items) > 0 || t.ReadAt != nil || t.Truncated
	if listed && !t.Consent.Active() {
		return invalidHomeProfile("templates are listed without an active consent")
	}
	if listed {
		if _, known := apps[t.AppID]; !known || t.ReadAt == nil {
			return invalidHomeProfile("templates name no listed app or no read time")
		}
	} else if t.AppID != "" && !homeProfileIDPattern.MatchString(t.AppID) {
		return invalidHomeProfile("templates app_id")
	}
	for _, item := range t.Items {
		if !homeProfileLine(item.Name, 120, false) || !HomeProfileTemplateFileValid(item.File) ||
			(item.Kind != HomeProfileTemplateProject && item.Kind != HomeProfileTemplateTrack) {
			return invalidHomeProfile("template item")
		}
	}
	switch t.Problem {
	case "":
		if t.ProblemAt != nil {
			return invalidHomeProfile("templates problem_at without a problem")
		}
	case HomeProfileTemplatesUnavailable, HomeProfileTemplatesFailed:
		if t.ProblemAt == nil {
			return invalidHomeProfile("templates problem without problem_at")
		}
	default:
		return invalidHomeProfile("templates problem")
	}
	return nil
}

// GetHomeProfile returns the Home's profile, or nil when it has none or the
// stored one does not validate.
func (state *AssistantProgramState) GetHomeProfile() *HomeProfile {
	if state == nil || state.HomeProfile == nil || state.HomeProfile.Validate() != nil {
		return nil
	}
	return state.HomeProfile.Clone()
}
