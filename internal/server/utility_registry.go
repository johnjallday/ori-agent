package server

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/config"
)

func buildUtilityToolRegistry(settings config.UtilitySettings) *chathttp.UtilityToolRegistry {
	if !settings.Enabled {
		return nil
	}

	policy := chathttp.DefaultUtilityCallPolicy()
	if settings.TimeoutMs > 0 {
		policy.Timeout = time.Duration(settings.TimeoutMs) * time.Millisecond
	}
	if settings.RetryAttempts >= 0 {
		policy.RetryAttempts = settings.RetryAttempts
	}
	if settings.RetryDelayMs > 0 {
		policy.RetryDelay = time.Duration(settings.RetryDelayMs) * time.Millisecond
	}

	clientTimeout := 8 * time.Second
	if policy.Timeout > 0 {
		clientTimeout = policy.Timeout + (2 * time.Second)
	}

	weatherAdapter := chathttp.NewOpenMeteoWeatherAdapter(&http.Client{Timeout: clientTimeout})
	if strings.TrimSpace(settings.WeatherGeocodingURL) != "" {
		weatherAdapter.GeocodingURL = strings.TrimSpace(settings.WeatherGeocodingURL)
	}
	if strings.TrimSpace(settings.WeatherForecastURL) != "" {
		weatherAdapter.ForecastURL = strings.TrimSpace(settings.WeatherForecastURL)
	}

	searchAdapter := buildWebSearchAdapter(settings, clientTimeout)

	webFetchCfg := chathttp.DefaultWebFetchAdapterConfig()
	if settings.WebFetchMaxResponseSize > 0 {
		webFetchCfg.MaxResponseBytes = settings.WebFetchMaxResponseSize
	}
	if strings.TrimSpace(settings.UserAgent) != "" {
		webFetchCfg.UserAgent = strings.TrimSpace(settings.UserAgent)
	}
	webFetchCfg.Safety.BlockPrivateHosts = settings.BlockPrivateHosts
	webFetchAdapter := chathttp.NewHTTPWebFetchAdapter(&http.Client{Timeout: clientTimeout}, webFetchCfg)

	browserPolicy := chathttp.DefaultBrowserAutomationPolicy()
	if settings.BrowserMaxResponseSize > 0 {
		browserPolicy.MaxResponseBytes = settings.BrowserMaxResponseSize
	}
	if strings.TrimSpace(settings.UserAgent) != "" {
		browserPolicy.UserAgent = strings.TrimSpace(settings.UserAgent)
	}
	browserPolicy.BlockPrivateHosts = settings.BlockPrivateHosts
	browserPolicy.AllowedDomains = append([]string{}, settings.BrowserAllowedDomains...)
	browserAdapter := chathttp.NewSimpleBrowserAutomationAdapter(&http.Client{Timeout: clientTimeout}, browserPolicy)

	return chathttp.NewUtilityToolRegistry(chathttp.UtilityAdapters{
		Time:      chathttp.SystemTimeAdapter{},
		Weather:   weatherAdapter,
		WebSearch: searchAdapter,
		WebFetch:  webFetchAdapter,
		Browser:   browserAdapter,
	}, policy)
}

func buildWebSearchAdapter(settings config.UtilitySettings, timeout time.Duration) chathttp.WebSearchAdapter {
	client := &http.Client{Timeout: timeout}
	searchProvider := strings.ToLower(strings.TrimSpace(settings.SearchProvider))
	braveKey := strings.TrimSpace(settings.BraveAPIKey)
	if braveKey == "" {
		braveKey = strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
	}

	switch searchProvider {
	case "brave":
		if braveKey != "" {
			return chathttp.NewBraveWebSearchAdapter(client, braveKey)
		}
		return chathttp.NewDuckDuckGoWebSearchAdapter(client)
	case "duckduckgo":
		return chathttp.NewDuckDuckGoWebSearchAdapter(client)
	case "", "auto":
		if braveKey != "" {
			return chathttp.NewBraveWebSearchAdapter(client, braveKey)
		}
		return chathttp.NewDuckDuckGoWebSearchAdapter(client)
	default:
		return chathttp.NewDuckDuckGoWebSearchAdapter(client)
	}
}
