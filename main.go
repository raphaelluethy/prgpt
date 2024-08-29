package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

var anthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

func main() {
	// Get current branch name
	currentBranch := getCommandOutput("git", "rev-parse", "--abbrev-ref", "HEAD")

	// Get base branch (usually main or master)
	baseBranch := strings.TrimPrefix(getCommandOutput("git", "rev-parse", "--abbrev-ref", "origin/HEAD"), "origin/")

	// Get list of commits
	commits := getCommandOutput("git", "log", fmt.Sprintf("%s..%s", baseBranch, currentBranch), "--pretty=format:%h - %s")

	// Get changes overview
	changesOverview := getCommandOutput("git", "diff", "--stat", fmt.Sprintf("%s..%s", baseBranch, currentBranch))

	// Prepare content for summarization
	content := fmt.Sprintf("Commits:\n%s\n\nChanges Overview:\n%s", commits, changesOverview)

	// Get summary from Anthropic API
	summary := getAnthropicSummary(content)

	// Generate PR summary
	prSummary := fmt.Sprintf(`# Pull Request Summary

## Branch: %s

## Commits:
%s

## Changes Overview:
%s

## Summary:
%s

## Detailed Description:
<!-- Please provide a detailed description of the changes in this PR -->
`, currentBranch, commits, changesOverview, summary)

	fmt.Println(prSummary)
}

func getCommandOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error executing command: %v\n", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(output))
}

func getAnthropicSummary(content string) string {
	prompt := fmt.Sprintf("Summarize the following Git commits and changes overview:\n\n%s\n\nProvide a concise summary of the changes:", content)

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":      "claude-3-5-sonnet-20240620",
		"max_tokens": 8096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, _ := http.NewRequest("POST", anthropicAPIURL, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", anthropicAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error calling Anthropic API: %v\n", err)
		return "Unable to generate summary"
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if text, ok := content[0].(map[string]interface{})["text"].(string); ok {
			return text
		}
	}

	return "Unable to generate summary"
}
