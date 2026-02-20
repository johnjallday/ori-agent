package chathttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenMeteoWeatherAdapter_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/geo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"results": [{
					"name":"Tokyo",
					"latitude":35.6762,
					"longitude":139.6503,
					"admin1":"Tokyo",
					"country":"Japan"
				}]
			}`))
		case "/forecast":
			if got := r.URL.Query().Get("latitude"); got == "" {
				http.Error(w, "missing latitude", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"current": {
					"time":"2026-02-19T12:00",
					"temperature_2m":12.3,
					"weather_code":2
				}
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter := NewOpenMeteoWeatherAdapter(server.Client())
	adapter.GeocodingURL = server.URL + "/geo"
	adapter.ForecastURL = server.URL + "/forecast"

	resp, err := adapter.GetWeather(context.Background(), WeatherRequest{
		Location: "Tokyo",
		Units:    "celsius",
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if resp.Location != "Tokyo, Japan" && resp.Location != "Tokyo, Tokyo, Japan" {
		t.Fatalf("unexpected location: %q", resp.Location)
	}
	if resp.Units != "celsius" {
		t.Fatalf("expected celsius units, got %q", resp.Units)
	}
	if resp.Source != "open-meteo.com" {
		t.Fatalf("expected source open-meteo.com, got %q", resp.Source)
	}
	if resp.Condition != "Partly cloudy" {
		t.Fatalf("expected condition Partly cloudy, got %q", resp.Condition)
	}
}

func TestOpenMeteoWeatherAdapter_LocationNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/geo" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	adapter := NewOpenMeteoWeatherAdapter(server.Client())
	adapter.GeocodingURL = server.URL + "/geo"
	adapter.ForecastURL = server.URL + "/forecast"

	_, err := adapter.GetWeather(context.Background(), WeatherRequest{Location: "Nowhere"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrUtilityInvalidInput) {
		t.Fatalf("expected ErrUtilityInvalidInput, got %v", err)
	}
}

func TestOpenMeteoWeatherAdapter_ProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"bad gateway"}`))
	}))
	defer server.Close()

	adapter := NewOpenMeteoWeatherAdapter(server.Client())
	adapter.GeocodingURL = server.URL + "/geo"
	adapter.ForecastURL = server.URL + "/forecast"

	_, err := adapter.GetWeather(context.Background(), WeatherRequest{Location: "Tokyo"})
	if err == nil {
		t.Fatalf("expected provider error")
	}
}
