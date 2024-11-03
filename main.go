package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

var anthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"
const ollamaAPIURL = "http://localhost:11434/api/embeddings"
const ollamaCompletionURL = "http://localhost:11434/api/generate"

type OllamaEmbeddingRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type OllamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

type OllamaCompletionRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type OllamaCompletionResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// main is the entry point of the program.
func main() {
	currentBranch := getCommandOutput("git", "rev-parse", "--abbrev-ref", "HEAD")

	// Get base branch (usually main or master)
	baseBranch := strings.TrimPrefix(getCommandOutput("git", "rev-parse", "--abbrev-ref", "origin/HEAD"), "origin/")

	if len(os.Args) > 1 {
		baseBranch = os.Args[1]
	}

	commits := getCommandOutput("git", "log", baseBranch+".."+currentBranch, "--pretty=format:%h - %s")

	detailedDiff := getCommandOutput("git", "diff", fmt.Sprintf("%s..%s", baseBranch, currentBranch))

	changesOverview := getCommandOutput("git", "diff", "--stat", fmt.Sprintf("%s..%s", baseBranch, currentBranch))

	content := fmt.Sprintf("Detailed Changes:\n%s\n\nChanges Overview:\n%s", detailedDiff, changesOverview)
	var summary string
	if len(commits) > 0 {
		summary = getAnthropicSummary(content)
	}

	// why is go string with multiline so ugly...
	prSummary := fmt.Sprintf(`# Pull Request Summary

## Branch: %s

## Commits:
%s

## Changes Overview:
%s

# Summary:
%s

## Detailed Description:
<!-- Please provide a detailed description of the changes in this PR -->
`, currentBranch, commits, changesOverview, summary)

	fmt.Println(prSummary)
}

// getCommandOutput executes a command and returns its output as a string.
func getCommandOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error executing command: %v\n", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(output))
}

// getEmbeddings sends a request to the Ollama API to generate embeddings for the given text.
// It returns the embeddings as a slice of float64 values and an error if any occurs.
func getEmbeddings(text string) ([]float64, error) {
	requestBody, err := json.Marshal(OllamaEmbeddingRequest{
		Model:  "nomic-embed-text",
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %v", err)
	}

	resp, err := http.Post(ollamaAPIURL, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("error calling Ollama API: %v", err)
	}
	defer resp.Body.Close()

	var result OllamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	return result.Embedding, nil
}

// processEmbeddings calculates the magnitude of the embeddings, normalizes them, and converts them to a base64 string.
func processEmbeddings(embeddings []float64) string {
	// Calculate magnitude
	var magnitude float64
	for _, v := range embeddings {
		magnitude += v * v
	}
	magnitude = math.Sqrt(magnitude)

	// Normalize embeddings
	normalized := make([]float64, len(embeddings))
	for i, v := range embeddings {
		normalized[i] = v / magnitude
	}

	// Convert to base64 for compact representation
	bytes, _ := json.Marshal(normalized)
	return base64.StdEncoding.EncodeToString(bytes)
}

// compressLogs sends a request to the Ollama API to compress and summarize the given content.
// It returns the compressed summary as a string and an error if any occurs.
func compressLogs(content string) (string, error) {
	prompt := fmt.Sprintf(`Compress and summarize the following git changes into a concise but informative format, 
preserving the most important technical details:

%s

Compressed summary:`, content)

	requestBody, err := json.Marshal(OllamaCompletionRequest{
		Model:  "llama2:3.2",
		Prompt: prompt,
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %v", err)
	}

	resp, err := http.Post(ollamaCompletionURL, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("error calling Ollama API: %v", err)
	}
	defer resp.Body.Close()

	var result OllamaCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("error decoding response: %v", err)
	}

	return result.Response, nil
}

// getAnthropicSummary generates a summary of the given content using the Anthropic API.
// It first compresses the logs, then gets embeddings for the compressed content, processes the embeddings,
// and finally generates a summary based on the processed embeddings and the original content.
func getAnthropicSummary(content string) string {
	// First compress the logs
	compressedContent, err := compressLogs(content)
	if err != nil {
		fmt.Printf("Error compressing logs: %v\n", err)
		compressedContent = content // Fallback to original content
	}

	// Get embeddings for the compressed content
	embeddings, err := getEmbeddings(compressedContent)
	if err != nil {
		fmt.Printf("Error getting embeddings: %v\n", err)
		return "Unable to generate summary"
	}

	// Process embeddings
	processedEmbeddings := processEmbeddings(embeddings)

	prompt := fmt.Sprintf(`Here are the Git changes with their semantic embeddings:

Embeddings: %s

Compressed Changes:
%s

Original Content Summary:
%s

Based on these changes, provide a concise summary of the modifications:`, processedEmbeddings, compressedContent, content)

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model": "claude-3-5-sonnet-latest",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 4096,
	})

	req, _ := http.NewRequest("POST", anthropicAPIURL, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", anthropicAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error calling Anthropic API: %v\n", err)
		return "Unable to generate summary"
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Debug the API response
	fmt.Printf("Anthropic API Response: %s\n", string(body))

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error unmarshaling response: %v\n", err)
		return "Unable to generate summary"
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text
	}

	return "Unable to generate summary"
}
