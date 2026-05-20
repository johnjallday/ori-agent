package orchestrationhttp

import "testing"

func TestParseOutputContractSuggestionNormalizesColumns(t *testing.T) {
	response, err := parseOutputContractSuggestion(`{
		"output_contract": {
			"source": "ai_suggested",
			"columns": [
				{"name": "date", "type": "date", "required": true, "description": "Run date"},
				{"name": "pollen_count", "type": "number", "required": true, "description": "Reported level"},
				{"name": "POLLEN_COUNT", "type": "number", "required": true}
			]
		},
		"reasoning": "Daily pollen runs need one row per observation."
	}`)
	if err != nil {
		t.Fatalf("parseOutputContractSuggestion returned error: %v", err)
	}
	if response.OutputContract == nil {
		t.Fatal("expected output contract")
	}
	if response.OutputContract.Source != "ai_suggested" {
		t.Fatalf("unexpected source: %q", response.OutputContract.Source)
	}
	if got := len(response.OutputContract.Columns); got != 2 {
		t.Fatalf("expected duplicate column to be removed, got %d columns", got)
	}
	if response.OutputContract.Columns[1].Name != "pollen_count" || response.OutputContract.Columns[1].Type != "number" {
		t.Fatalf("unexpected second column: %+v", response.OutputContract.Columns[1])
	}
}

func TestParseOutputContractSuggestionRejectsMissingContract(t *testing.T) {
	if _, err := parseOutputContractSuggestion(`{"reasoning":"no columns"}`); err == nil {
		t.Fatal("expected missing contract to fail")
	}
}
