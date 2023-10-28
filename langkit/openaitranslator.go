package langkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonelquinteros/gotext"
)

func TranslateMissingWithOpenAI(apiKey string, potFile string, targetPoFilesGlob string) {
	fmt.Println("translating missing strings in .po files using OpenAI")
	fmt.Println("source: " + potFile)
	// read the source file
	source := gotext.NewPo()
	bytes, err := os.ReadFile(potFile)
	if err != nil {
		panic(fmt.Errorf("Could not read .pot file at %v. Err: %w", potFile, err))
	}
	source.Parse(bytes)

	// read the targets
	targets, err := filepath.Glob(targetPoFilesGlob)
	if err != nil {
		panic(err)
	}

	esc := func(input string) string {
		input = strings.Replace(input, "\"", "\\\"", -1)
		input = strings.Replace(input, "\n", "\\n", -1)
		return input
	}
	eq := func(a, b string) bool {
		result := a == b
		return result
	}

	// translate each target
	for _, targetPath := range targets {
		fmt.Println(" file: " + targetPath)

		// remove file extension
		filenameWithExt := filepath.Base(targetPath)                          // Step 1: Get the filename with extension
		extension := filepath.Ext(filenameWithExt)                            // Step 2: Get the extension
		targetLocale := filenameWithExt[:len(filenameWithExt)-len(extension)] // Step 3: Remove the extension

		// read the target file
		target := gotext.NewPo()
		bytes, err := os.ReadFile(targetPath)
		if err != nil {
			panic(fmt.Errorf("Could not read .pot file at %v. Err: %w", potFile, err))
		}
		target.Parse(bytes)

		for _, s := range source.GetDomain().GetTranslations() {
			found := false
			for _, t := range target.GetDomain().GetTranslations() {
				if eq(s.ID, t.ID) && eq(s.PluralID, t.PluralID) {
					if s.PluralID == "" {
						target.GetDomain().Set(esc(s.ID), esc(t.Trs[0]))
					} else {
						panic("OpenAI Auto Translate doesn't yet work with plural forms")
					}
					found = true
					break
				}
			}

			if !found {
				// create the translation
				if s.PluralID == "" {
					fmt.Println("  - translating: \"" + strings.Replace(s.ID, "\n", "\\n", -1) + "\"")
					translation, err := openAI("You are a skilled translator for software as a service tools. You create the most acurate and concise translations possible. Do not print anything except the final translated text.", fmt.Sprintf("Translate the following to %v: %v", targetLocale, s.ID), apiKey, "gpt-4")
					if err != nil {
						panic(err)
					}
					fmt.Println("       - result:", "\""+strings.Replace(translation, "\n", "\\n", -1)+"\"")
					target.GetDomain().Set(esc(s.ID), esc(translation))
				} else {
					panic("OpenAI Auto Translate doesn't yet work with plural forms")
				}
			}
		}

		text, err := target.MarshalText()
		if err != nil {
			panic(err)
		}
		os.WriteFile(targetPath, text, 0644)
	}
}

func openAI(system string, user string, apiKey string, model string) (string, error) {
	// Request structures
	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type CompletionRequest struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
	}

	// Response structures
	type Choice struct {
		Index   int     `json:"index"`
		Message Message `json:"message"`
	}

	type Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}

	type CompletionResponse struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Created int      `json:"created"`
		Model   string   `json:"model"`
		Choices []Choice `json:"choices"`
		Usage   Usage    `json:"usage"`
	}
	// Create request payload
	reqPayload := CompletionRequest{
		Model: model,
		Messages: []Message{
			{
				Role:    "system",
				Content: system,
			},
			{
				Role:    "user",
				Content: user,
			},
		},
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", err
	}

	// Make the HTTP request
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse the response payload
	var completionResp CompletionResponse
	err = json.Unmarshal(respBody, &completionResp)
	if err != nil {
		return "", err
	}

	// Print the assistant's message
	return completionResp.Choices[0].Message.Content, nil
}
