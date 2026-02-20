package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openMeteoGeocodingURL  = "https://geocoding-api.open-meteo.com/v1/search"
	openMeteoForecastURL   = "https://api.open-meteo.com/v1/forecast"
	openMeteoAirQualityURL = "https://air-quality-api.open-meteo.com/v1/air-quality"
)

// OpenMeteoWeatherAdapter is a no-key weather adapter using Open-Meteo APIs.
type OpenMeteoWeatherAdapter struct {
	HTTPClient    *http.Client
	GeocodingURL  string
	ForecastURL   string
	AirQualityURL string
}

// NewOpenMeteoWeatherAdapter creates an Open-Meteo weather adapter.
func NewOpenMeteoWeatherAdapter(client *http.Client) *OpenMeteoWeatherAdapter {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &OpenMeteoWeatherAdapter{
		HTTPClient:    client,
		GeocodingURL:  openMeteoGeocodingURL,
		ForecastURL:   openMeteoForecastURL,
		AirQualityURL: openMeteoAirQualityURL,
	}
}

type openMeteoGeocodeResponse struct {
	Results []struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Admin1    string  `json:"admin1"`
		Country   string  `json:"country"`
	} `json:"results"`
}

type openMeteoForecastResponse struct {
	Current struct {
		Time        string  `json:"time"`
		Temperature float64 `json:"temperature_2m"`
		WeatherCode int     `json:"weather_code"`
	} `json:"current"`
}

type openMeteoAirQualityResponse struct {
	Current struct {
		Time  string   `json:"time"`
		USAQI *float64 `json:"us_aqi"`
		EUAQI *float64 `json:"european_aqi"`
		PM25  *float64 `json:"pm2_5"`
		PM10  *float64 `json:"pm10"`
	} `json:"current"`
}

// GetWeather retrieves weather from Open-Meteo geocoding + forecast APIs.
func (a *OpenMeteoWeatherAdapter) GetWeather(ctx context.Context, req WeatherRequest) (WeatherResponse, error) {
	location := strings.TrimSpace(req.Location)
	if location == "" {
		return WeatherResponse{}, fmt.Errorf("%w: location is required", ErrUtilityInvalidInput)
	}
	if a == nil || a.HTTPClient == nil {
		return WeatherResponse{}, fmt.Errorf("%w: weather adapter not configured", ErrUtilityProviderUnavailable)
	}

	units := normalizeWeatherUnits(req.Units)

	geocodeData, err := a.geocodeLocation(ctx, location)
	if err != nil {
		return WeatherResponse{}, err
	}
	if len(geocodeData.Results) == 0 {
		return WeatherResponse{}, fmt.Errorf("%w: location not found", ErrUtilityInvalidInput)
	}
	target := geocodeData.Results[0]

	forecast, err := a.fetchForecast(ctx, target.Latitude, target.Longitude, units)
	if err != nil {
		return WeatherResponse{}, err
	}

	return WeatherResponse{
		Location:    formatWeatherLocation(target.Name, target.Admin1, target.Country),
		Temperature: forecast.Current.Temperature,
		Units:       units,
		Condition:   openMeteoWeatherCodeToText(forecast.Current.WeatherCode),
		Source:      "open-meteo.com",
		ObservedAt:  forecast.Current.Time,
	}, nil
}

// GetAirQuality retrieves air quality from Open-Meteo geocoding + air quality APIs.
func (a *OpenMeteoWeatherAdapter) GetAirQuality(ctx context.Context, req AirQualityRequest) (AirQualityResponse, error) {
	location := strings.TrimSpace(req.Location)
	if location == "" {
		return AirQualityResponse{}, fmt.Errorf("%w: location is required", ErrUtilityInvalidInput)
	}
	if a == nil || a.HTTPClient == nil {
		return AirQualityResponse{}, fmt.Errorf("%w: air quality adapter not configured", ErrUtilityProviderUnavailable)
	}

	standard := normalizeAirQualityStandard(req.Standard)

	geocodeData, err := a.geocodeLocation(ctx, location)
	if err != nil {
		return AirQualityResponse{}, err
	}
	if len(geocodeData.Results) == 0 {
		return AirQualityResponse{}, fmt.Errorf("%w: location not found", ErrUtilityInvalidInput)
	}
	target := geocodeData.Results[0]

	airQuality, err := a.fetchAirQuality(ctx, target.Latitude, target.Longitude, standard)
	if err != nil {
		return AirQualityResponse{}, err
	}

	aqi := 0.0
	if standard == "eu" {
		if airQuality.Current.EUAQI == nil {
			return AirQualityResponse{}, fmt.Errorf("air quality provider returned incomplete data")
		}
		aqi = *airQuality.Current.EUAQI
	} else {
		if airQuality.Current.USAQI == nil {
			return AirQualityResponse{}, fmt.Errorf("air quality provider returned incomplete data")
		}
		aqi = *airQuality.Current.USAQI
	}

	pm25 := 0.0
	if airQuality.Current.PM25 != nil {
		pm25 = *airQuality.Current.PM25
	}
	pm10 := 0.0
	if airQuality.Current.PM10 != nil {
		pm10 = *airQuality.Current.PM10
	}

	return AirQualityResponse{
		Location:   formatWeatherLocation(target.Name, target.Admin1, target.Country),
		AQI:        aqi,
		Scale:      standard,
		Category:   airQualityCategory(aqi, standard),
		PM25:       pm25,
		PM10:       pm10,
		Source:     "open-meteo.com",
		ObservedAt: airQuality.Current.Time,
	}, nil
}

func (a *OpenMeteoWeatherAdapter) geocodeLocation(ctx context.Context, location string) (*openMeteoGeocodeResponse, error) {
	values := url.Values{}
	values.Set("name", location)
	values.Set("count", "1")
	values.Set("language", "en")
	values.Set("format", "json")

	body, err := a.doJSONRequest(ctx, a.GeocodingURL, values)
	if err != nil {
		return nil, err
	}

	var payload openMeteoGeocodeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse geocoding response: %w", err)
	}
	return &payload, nil
}

func (a *OpenMeteoWeatherAdapter) fetchForecast(ctx context.Context, latitude, longitude float64, units string) (*openMeteoForecastResponse, error) {
	values := url.Values{}
	values.Set("latitude", fmt.Sprintf("%.6f", latitude))
	values.Set("longitude", fmt.Sprintf("%.6f", longitude))
	values.Set("current", "temperature_2m,weather_code")
	values.Set("timezone", "auto")
	if units == "fahrenheit" {
		values.Set("temperature_unit", "fahrenheit")
	}

	body, err := a.doJSONRequest(ctx, a.ForecastURL, values)
	if err != nil {
		return nil, err
	}

	var payload openMeteoForecastResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse forecast response: %w", err)
	}
	return &payload, nil
}

func (a *OpenMeteoWeatherAdapter) fetchAirQuality(ctx context.Context, latitude, longitude float64, standard string) (*openMeteoAirQualityResponse, error) {
	values := url.Values{}
	values.Set("latitude", fmt.Sprintf("%.6f", latitude))
	values.Set("longitude", fmt.Sprintf("%.6f", longitude))
	if standard == "eu" {
		values.Set("current", "european_aqi,pm2_5,pm10")
	} else {
		values.Set("current", "us_aqi,pm2_5,pm10")
	}
	values.Set("timezone", "auto")

	body, err := a.doJSONRequest(ctx, a.AirQualityURL, values)
	if err != nil {
		return nil, err
	}

	var payload openMeteoAirQualityResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse air quality response: %w", err)
	}
	return &payload, nil
}

func (a *OpenMeteoWeatherAdapter) doJSONRequest(ctx context.Context, baseURL string, query url.Values) ([]byte, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid weather endpoint: %w", err)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create weather request: %w", err)
	}

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("weather provider request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, fmt.Errorf("failed to read weather provider response: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("weather provider returned HTTP %d", resp.StatusCode)
	}

	return body, nil
}

func normalizeWeatherUnits(units string) string {
	switch strings.ToLower(strings.TrimSpace(units)) {
	case "f", "fahrenheit":
		return "fahrenheit"
	default:
		return "celsius"
	}
}

func normalizeAirQualityStandard(standard string) string {
	switch strings.ToLower(strings.TrimSpace(standard)) {
	case "eu", "european":
		return "eu"
	default:
		return "us"
	}
}

func airQualityCategory(aqi float64, standard string) string {
	if strings.EqualFold(standard, "eu") {
		switch {
		case aqi <= 20:
			return "Good"
		case aqi <= 40:
			return "Fair"
		case aqi <= 60:
			return "Moderate"
		case aqi <= 80:
			return "Poor"
		case aqi <= 100:
			return "Very poor"
		default:
			return "Extremely poor"
		}
	}

	switch {
	case aqi <= 50:
		return "Good"
	case aqi <= 100:
		return "Moderate"
	case aqi <= 150:
		return "Unhealthy for sensitive groups"
	case aqi <= 200:
		return "Unhealthy"
	case aqi <= 300:
		return "Very unhealthy"
	default:
		return "Hazardous"
	}
}

func formatWeatherLocation(name, admin1, country string) string {
	parts := []string{}
	for _, part := range []string{name, admin1, country} {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		duplicate := false
		for _, existing := range parts {
			if strings.EqualFold(existing, part) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

func openMeteoWeatherCodeToText(code int) string {
	switch code {
	case 0:
		return "Clear sky"
	case 1:
		return "Mainly clear"
	case 2:
		return "Partly cloudy"
	case 3:
		return "Overcast"
	case 45, 48:
		return "Fog"
	case 51, 53, 55:
		return "Drizzle"
	case 56, 57:
		return "Freezing drizzle"
	case 61, 63, 65:
		return "Rain"
	case 66, 67:
		return "Freezing rain"
	case 71, 73, 75, 77:
		return "Snow"
	case 80, 81, 82:
		return "Rain showers"
	case 85, 86:
		return "Snow showers"
	case 95:
		return "Thunderstorm"
	case 96, 99:
		return "Thunderstorm with hail"
	default:
		return "Unknown"
	}
}
