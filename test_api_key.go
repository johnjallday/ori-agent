package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("❌ OPENAI_API_KEY not set")
		os.Exit(1)
	}

	fmt.Printf("✓ API Key set (length: %d)\n", len(apiKey))
	fmt.Printf("✓ Key starts with: %s\n", apiKey[:10])

	// Create client
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
	)

	// Test 1: List models
	fmt.Println("\n📋 Testing models list...")
	ctx := context.Background()
	iter := client.Models.ListAutoPaging(ctx)

	modelCount := 0
	for iter.Next() {
		model := iter.Current()
		if modelCount < 5 {
			fmt.Printf("  - %s\n", model.ID)
		}
		modelCount++
	}

	if err := iter.Err(); err != nil {
		fmt.Printf("❌ Error listing models: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Found %d models\n", modelCount)

	// Test 2: Simple chat completion
	fmt.Println("\n💬 Testing chat completion with gpt-4o...")
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel("gpt-4o"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say 'test' in one word"),
		},
		MaxTokens: openai.Int(5),
	})

	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Success! Response: %s\n", completion.Choices[0].Message.Content)
	fmt.Println("\n✅ All tests passed! Your API key works correctly.")
}
