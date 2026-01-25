package speechhttp

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const (
	maxAudioSize        = 25 << 20 // 25 MB
	defaultOpenAIModel  = "gpt-4o-mini-transcribe"
	fallbackOpenAIModel = "whisper-1"
)

// Handler provides speech-related HTTP handlers.
type Handler struct {
	configManager *config.Manager
}

// NewHandler creates a new speech HTTP handler.
func NewHandler(configManager *config.Manager) *Handler {
	return &Handler{configManager: configManager}
}

// Transcribe handles POST /api/transcribe
func (h *Handler) Transcribe(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioSize)
	if err := r.ParseMultipartForm(maxAudioSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			orihttp.BadRequest(w, "Audio file too large")
			return
		}
		orihttp.BadRequest(w, "Failed to parse audio upload: "+err.Error())
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		file, header, err = r.FormFile("file")
	}
	if err != nil {
		orihttp.BadRequest(w, "Audio file is required")
		return
	}
	defer func() { _ = file.Close() }()

	cfg := h.configManager.Get()

	provider := strings.TrimSpace(r.FormValue("provider"))
	if provider == "" || provider == "auto" {
		provider = strings.TrimSpace(cfg.SpeechProvider)
	}
	if provider == "" || provider == "auto" {
		if h.configManager.GetAPIKey() == "" {
			orihttp.BadRequest(w, "OpenAI API key required for server transcription")
			return
		}
		provider = "openai"
	}

	if provider == "browser" || provider == "off" {
		orihttp.BadRequest(w, "Selected speech provider is not available on the server")
		return
	}
	if provider != "openai" {
		orihttp.BadRequest(w, "Unsupported speech provider")
		return
	}

	apiKey := h.configManager.GetAPIKey()
	if apiKey == "" {
		orihttp.BadRequest(w, "OpenAI API key required for transcription")
		return
	}

	model := strings.TrimSpace(r.FormValue("model"))
	if model == "" {
		model = strings.TrimSpace(cfg.SpeechModel)
	}
	if model == "" {
		model = defaultOpenAIModel
	}

	language := strings.TrimSpace(r.FormValue("language"))
	if language == "" {
		language = strings.TrimSpace(cfg.SpeechLanguage)
	}
	language = normalizeLanguage(language)

	httpClient := llm.NewHTTPClient(llm.DefaultCloudTimeout)
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient),
	)

	params := openai.AudioTranscriptionNewParams{
		File:           file,
		Model:          openai.AudioModel(model),
		ResponseFormat: openai.AudioResponseFormatJSON,
	}
	if language != "" {
		params.Language = openai.String(language)
	}

	resp, err := client.Audio.Transcriptions.New(r.Context(), params)
	if err != nil {
		logger.Warn("Transcription failed", logger.Fields{"error": err.Error(), "provider": provider})
		orihttp.InternalError(w, "Transcription failed: "+err.Error())
		return
	}
	if resp == nil {
		orihttp.InternalError(w, "Transcription failed: empty response")
		return
	}

	transcription := resp.AsTranscription()
	text := strings.TrimSpace(transcription.Text)
	if text == "" {
		orihttp.InternalError(w, "Transcription returned empty text")
		return
	}

	modelUsed := model
	if modelUsed == "" {
		modelUsed = fallbackOpenAIModel
	}

	_ = header
	orihttp.WriteJSON(w, map[string]interface{}{
		"text":     text,
		"provider": provider,
		"model":    modelUsed,
		"language": language,
	})
}

func normalizeLanguage(language string) string {
	language = strings.TrimSpace(strings.ToLower(language))
	if language == "" || language == "auto" {
		return ""
	}
	parts := strings.Split(language, "-")
	if len(parts) > 0 {
		language = parts[0]
	}
	if len(language) > 2 {
		return language[:2]
	}
	return language
}
