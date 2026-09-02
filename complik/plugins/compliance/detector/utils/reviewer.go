// Copyright 2025 CompliK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package utils provides utility functions and types for compliance detection,
// including an AI-powered content reviewer that analyzes website content for
// compliance violations using language models.
package utils

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bearslyricattack/CompliK/complik/pkg/logger"
	"github.com/bearslyricattack/CompliK/complik/pkg/models"
)

type ContentReviewer struct {
	log    logger.Logger
	apiKey string
	apiURL string
	model  string
}

func NewContentReviewer(
	log logger.Logger,
	apiKey, apiBase, apiPath, model string,
) *ContentReviewer {
	apiURL := apiBase + apiPath

	return &ContentReviewer{
		log:    log,
		apiKey: apiKey,
		apiURL: apiURL,
		model:  model,
	}
}

func (r *ContentReviewer) ReviewSiteContent(
	ctx context.Context,
	content *models.CollectorInfo,
	name string,
	customRules []CustomKeywordRule,
	safetyPrompt string,
) (*models.DetectorInfo, error) {
	if content == nil {
		r.log.Error("Review called with nil content")
		return nil, errors.New("ScrapeResult parameter is nil")
	}

	r.log.Debug("Preparing review request", logger.Fields{
		"host":             content.Host,
		"has_custom_rules": len(customRules) > 0,
		"has_safety_rules": strings.TrimSpace(safetyPrompt) != "",
	})

	requestData := r.prepareRequestData(content, customRules, safetyPrompt)

	r.log.Debug("Calling review API", logger.Fields{
		"api_url": r.apiURL,
		"model":   r.model,
	})

	response, err := r.callAPI(ctx, requestData)
	if err != nil {
		r.log.Error("API call failed", logger.Fields{
			"error": err.Error(),
			"host":  content.Host,
		})

		return nil, fmt.Errorf("failed to call API: %w", err)
	}

	r.log.Debug("Parsing API response")

	result, err := r.parseResponse(response, content, name)
	if err != nil {
		r.log.Error("Failed to parse response", logger.Fields{
			"error": err.Error(),
			"host":  content.Host,
		})

		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	r.log.Debug("Review completed", logger.Fields{
		"host":           content.Host,
		"is_illegal":     result.IsIllegal,
		"keywords_count": len(result.Keywords),
	})

	return result, nil
}

func (r *ContentReviewer) prepareRequestData(
	content *models.CollectorInfo,
	customRules []CustomKeywordRule,
	safetyPrompt string,
) map[string]any {
	base64Image := base64.StdEncoding.EncodeToString(content.Screenshot)
	htmlContent := content.HTML

	originalLength := len(htmlContent)
	if len(htmlContent) > 10000 {
		htmlContent = htmlContent[:10000] + "..."

		r.log.Debug("HTML content truncated", logger.Fields{
			"original_length": originalLength,
			"truncated_to":    10000,
		})
	}

	var prompt string
	switch {
	case len(customRules) > 0:
		prompt = r.buildCustomPrompt(htmlContent, customRules)
	case strings.TrimSpace(safetyPrompt) != "":
		prompt = r.buildSafetyPromptFromRules(htmlContent, safetyPrompt)
	default:
		prompt = r.buildPrompt(htmlContent)
	}

	requestData := map[string]any{
		"model": r.model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": "data:image/png;base64," + base64Image,
						},
					},
				},
			},
		},
		"max_completion_tokens": 6000,
		"response_format":       ReviewResultSchema,
	}

	return requestData
}

func (r *ContentReviewer) buildSafetyPromptFromRules(
	htmlContent string,
	safetyPrompt string,
) string {
	htmlBlock := "```html\n" + htmlContent + "\n```"

	return fmt.Sprintf(`# Role: Content Analysis and Compliance Checker

# Goal:
1. Provide an accurate one-sentence description of the webpage's visible content, service, product, or purpose.
2. Extract up to 5 English matched keywords or evidence phrases that triggered the safety rules.
3. Determine whether the webpage contains illegal or non-compliant content according to the safety rules below, and identify the exact violating content locations and evidence.

# Safety Rules:
%s

# Important Notes:
I am providing you with both a webpage screenshot and HTML code. Please analyze both sources comprehensively. Some content may be more obvious in the screenshot, while other content may need to be analyzed from the HTML code.
If the page shows 404 errors, various errors, blank pages, or missing resources, it should be considered compliant.
Output language requirements:
- description must be Simplified Chinese and must precisely describe the webpage content, business, product, or function.
- keywords must be English matched risk keywords or evidence phrases found in the screenshot or HTML. Use an empty array when no rule is matched.
- compliance.explanation must be Simplified Chinese and should state the violated category, exact matched text, UI location or HTML evidence, and why that evidence violates the rules.

# HTML Code Excerpt:
%s

# Output:
Please output strictly in the following JSON format without any additional explanation or text:

{
  "description": "<简体中文网页内容精准描述>",
  "keywords": ["<matched_keyword1>", "<matched_keyword2>", "<matched_keyword3>", "<matched_keyword4>", "<matched_keyword5>"],
  "compliance": {
    "is_illegal": "<Yes/No>",
    "explanation": "<简体中文违规依据，说明违规类型、具体违规位置、命中文案或HTML证据，以及违规原因>"
  }
}`, strings.TrimSpace(safetyPrompt), htmlBlock)
}

func (r *ContentReviewer) buildPrompt(htmlContent string) string {
	return `# Role: Content Analysis and Compliance Checker

# Goal:
1. Provide an accurate one-sentence description of the given webpage's visible content, service, product, or purpose.
2. Extract up to 5 English matched keywords or evidence phrases that indicate risk.
3. Determine whether the webpage contains content that violates Chinese laws and regulations, particularly in the following categories: pornography, political sensitivity, prohibited items, gambling, cult activities, violence/terrorism, fraud, and infringement, and identify the exact violating content locations and evidence.

# Instructions:
1. **Content Description**: Based on the HTML file and webpage screenshot, generate a precise one-sentence summary describing the webpage's visible content, business, product, or function. Include the platform name, service type, key functions, and payment or transaction method when they are visible.

2. **Matched Keyword Extraction**: Extract up to 5 English keywords or short evidence phrases that directly indicate the matched risk. Prefer exact visible text, button text, titles, navigation labels, payment words, action words, HTML text, or other phrases that support the compliance decision.

3. **Compliance Assessment**: Analyze the webpage content to determine if it contains the following illegal or non-compliant content, and provide a concrete explanation with location and evidence.
   - **Pornographic Content**: Are there any sexually explicit images, text, or videos?
   - **Political Sensitivity**: Is there politically sensitive information or criticism of the Chinese government?
   - **Prohibited Items**: Are there any items, behaviors, or services prohibited by Chinese law?
   - **Gambling Content**: Does it involve gambling activities or advertisements?
   - **Cult Content**: Does it promote cult or extreme religious ideology?
   - **Violence/Terrorism**: Does it contain violent or terrorist content?
   - **Fraud Content**: Does it contain online fraud content?
   - **Infringement Content**: Does it contain infringing content?
   - **Fraud Detection**: Pay special attention to chat pages; if it's a chat page, determine whether it involves suspected fraud.

# Important Notes:
I am providing you with both a webpage screenshot and HTML code. Please analyze both sources comprehensively. Some content may be more obvious in the screenshot, while other content may need to be analyzed from the HTML code. Stay vigilant; even seemingly normal websites may hide non-compliant content in the code.
Pay special attention to social platforms like Weibo, WeChat, Douyin, Kuaishou, Xiaohongshu, and other well-known platforms to prevent fraudulent content. Also be particularly alert for keywords related to gambling, pornography, political sensitivity, violence/terrorism, and cult activities.

## Special Reminder
If the page shows 404 errors, various errors, blank pages, or missing resources, it should be considered compliant.
Output language requirements:
- description must be Simplified Chinese and must precisely describe the webpage content, business, product, or function.
- keywords must be English matched risk keywords or evidence phrases found in the screenshot or HTML. Use an empty array when no risk is matched.
- compliance.explanation must be Simplified Chinese and should state the violated category, exact matched text, UI location or HTML evidence, and why that evidence violates the rules.

# HTML Code Excerpt:
` + "```html\n" + htmlContent + "\n```" + `

# Output:
Please output strictly in the following JSON format without any additional explanation or text:

{
  "description": "<简体中文网页内容精准描述>",
  "keywords": ["<matched_keyword1>", "<matched_keyword2>", "<matched_keyword3>", "<matched_keyword4>", "<matched_keyword5>"],
  "compliance": {
    "is_illegal": "<Yes/No>",
    "explanation": "<简体中文违规依据，说明违规类型、具体违规位置、命中文案或HTML证据，以及违规原因>"
  }
}`
}

func (r *ContentReviewer) buildRulesDescription(rules []CustomKeywordRule) string {
	var builder strings.Builder
	for _, rule := range rules {
		keywords := strings.FieldsFunc(rule.Keywords, func(r rune) bool {
			switch r {
			case '.', ',', '，', '、', ';', '；', '\n', '\r':
				return true
			default:
				return false
			}
		})

		cleanedKeywords := make([]string, 0, len(keywords))
		for _, keyword := range keywords {
			trimmed := strings.TrimSpace(keyword)
			if trimmed != "" {
				cleanedKeywords = append(cleanedKeywords, trimmed)
			}
		}

		ruleType := strings.TrimSpace(rule.Type)
		if ruleType == "" {
			ruleType = "custom"
		}

		description := strings.TrimSpace(rule.Description)
		if description == "" {
			description = ruleType + "关键词检测规则"
		}

		ruleText := fmt.Sprintf(
			"类型: %s\n说明: %s\n关键词: %s\n",
			ruleType,
			description,
			strings.Join(cleanedKeywords, ", "),
		)

		builder.WriteString(ruleText)
		builder.WriteString("\n")
	}

	return strings.TrimSpace(builder.String())
}

func (r *ContentReviewer) buildCustomPrompt(
	htmlContent string,
	customRules []CustomKeywordRule,
) string {
	rulesDescription := r.buildRulesDescription(customRules)

	return fmt.Sprintf(`# Role: Intelligent Webpage Content Compliance Detection Expert

# Task Objective:
Conduct a comprehensive analysis of the provided webpage content, focusing on detecting custom keyword rules, and output results strictly in JSON format.

# Analysis Requirements:

## 1. Content Description
- Based on HTML code and screenshot analysis, provide a one-sentence concise summary of the webpage's visible content, business, product, or function
- The description should be accurate, objective, and no more than 80 Chinese characters
- The description must be written in Simplified Chinese

## 2. Matched Keyword Extraction
- Extract matched keywords or short evidence phrases that directly hit the custom rules
- Output keywords as a JSON array of English strings, up to 5 items
- Keywords should prefer exact matched words, visible text, button text, titles, navigation labels, payment words, action words, HTML text, or other evidence phrases

## 3. Custom Rule Detection
Please strictly detect according to the following custom rules:

%s

## Detection Instructions:
- Carefully analyze the text content in the HTML code
- Check each custom rule one by one
- Record all matching keywords and corresponding rules

# HTML Code:
%s

# Important Notes:
I am providing you with both a webpage screenshot and HTML code. Please analyze both sources comprehensively. Some content may be more obvious in the screenshot, while other content may need to be analyzed from the HTML code. Stay vigilant; even seemingly normal websites may hide non-compliant content in the code.
If the page shows access errors, is blank, or resources do not exist, it should be considered compliant.
Output language requirements:
- description must be Simplified Chinese and must precisely describe the webpage content, business, product, or function.
- keywords must be English matched risk keywords or evidence phrases found in the screenshot or HTML. Use an empty array when no custom rule is matched.
- compliance.explanation must be Simplified Chinese and should state the matched custom rule type, exact matched text, UI location or HTML evidence, and why that evidence violates the custom rule.

# Output Requirements:
Please output strictly in the following JSON format without any additional explanation or text:

{
  "description": "<简体中文网页内容精准描述>",
  "keywords": ["<matched_keyword1>", "<matched_keyword2>", "<matched_keyword3>", "<matched_keyword4>", "<matched_keyword5>"],
  "compliance": {
    "is_illegal": "<Yes/No>",
    "explanation": "<简体中文违规依据，说明命中的自定义规则类型、具体违规位置、命中文案或HTML证据，以及违规原因>"
  }
}

Notes:
- compliance.is_illegal: Yes indicates non-compliant content found, No indicates compliant content
- keywords: Up to 5 English matched risk keywords or evidence phrases as a JSON array
- description: Precise one-sentence Simplified Chinese webpage content description
- compliance.explanation: Simplified Chinese explanation of matched rule types, exact locations, matched text, HTML or screenshot evidence, and violation reason`, rulesDescription, htmlContent)
}

func (r *ContentReviewer) callAPI(
	ctx context.Context,
	requestData map[string]any,
) (*APIResponse, error) {
	requestBody, err := json.Marshal(requestData)
	if err != nil {
		r.log.Error("Failed to serialize request data", logger.Fields{
			"error": err.Error(),
		})
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.apiURL,
		strings.NewReader(string(requestBody)),
	)
	if err != nil {
		r.log.Error("Failed to create HTTP request", logger.Fields{
			"error": err.Error(),
			"url":   r.apiURL,
		})

		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	r.log.Debug("Sending HTTP request", logger.Fields{
		"url":             r.apiURL,
		"timeout_seconds": 180,
	})

	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		r.log.Error("Failed to send HTTP request", logger.Fields{
			"error": err.Error(),
			"url":   r.apiURL,
		})

		return nil, err
	}
	defer func(Body io.ReadCloser) {
		if err := Body.Close(); err != nil {
			r.log.Error("Failed to close response body", logger.Fields{
				"error": err.Error(),
			})
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errorText := string(body)
		r.log.Error("API call failed with non-200 status", logger.Fields{
			"status_code": resp.StatusCode,
			"error_text":  errorText,
			"url":         r.apiURL,
		})

		return nil, fmt.Errorf("API call failed: status code %d", resp.StatusCode)
	}

	var responseData APIResponse
	if err := json.Unmarshal(body, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	if len(responseData.Choices) == 0 {
		r.log.Error("API response has no choices")
		return nil, errors.New("no results in API response")
	}

	r.log.Debug("API call successful", logger.Fields{
		"choices_count": len(responseData.Choices),
	})

	return &responseData, nil
}

func (r *ContentReviewer) parseResponse(
	response *APIResponse,
	content *models.CollectorInfo,
	name string,
) (*models.DetectorInfo, error) {
	reviewResult := response.Choices[0].Message.Content
	cleanData := r.cleanResponseData(reviewResult)

	var result ReviewResult
	if err := json.Unmarshal([]byte(cleanData), &result); err != nil {
		r.log.Error("Failed to parse API response JSON", logger.Fields{
			"error":           err.Error(),
			"raw_data_length": len(cleanData),
		})

		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	keywords := result.Keywords
	if keywords == nil {
		keywords = []string{}
	}

	isIllegal := result.Compliance.IsIllegal == "Yes"

	explanation := result.Compliance.Explanation
	if explanation == "" {
		explanation = "No specific explanation"
	}

	return &models.DetectorInfo{
		DiscoveryName: content.DiscoveryName,
		CollectorName: content.CollectorName,
		DetectorName:  name,
		ReviewTaskID:  content.ReviewTaskID,
		Name:          content.Name,
		Namespace:     content.Namespace,
		Host:          content.Host,
		Path:          content.Path,
		URL:           content.URL,
		DeviceProfile: content.DeviceProfile,
		Viewport:      content.Viewport,
		IsIllegal:     isIllegal,
		Description:   result.Description,
		Keywords:      keywords,
		Explanation:   explanation,
	}, nil
}

func (r *ContentReviewer) cleanResponseData(data string) string {
	re := regexp.MustCompile(`(\d+\.\s+\d+)`)
	return re.ReplaceAllStringFunc(data, func(match string) string {
		return strings.ReplaceAll(match, " ", "")
	})
}

type CustomKeywordRule struct {
	Type        string `json:"type"`
	Keywords    string `json:"keywords"`
	Description string `json:"description"`
}

type CustomComplianceResult struct {
	IsCompliant   bool     `json:"is_compliant"`
	Keywords      string   `json:"keywords"`
	Description   string   `json:"description"`
	ViolatedTypes []string `json:"violated_types,omitempty"` // List of violated types
}

type ReviewResult struct {
	Description string     `json:"description"`
	Keywords    []string   `json:"keywords"`
	Compliance  Compliance `json:"compliance"`
}

type Compliance struct {
	IsIllegal   string `json:"is_illegal"`
	Explanation string `json:"explanation"`
}

var ReviewResultSchema = map[string]any{
	"type": "json_schema",
	"json_schema": map[string]any{
		"name":   "review_result",
		"strict": true,
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{
					"type":        "string",
					"description": "Precise Simplified Chinese one-sentence description of webpage content, business, product, or function",
				},
				"keywords": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"maxItems":    5,
					"description": "English matched risk keywords or evidence phrases found in the screenshot or HTML, up to 5",
				},
				"compliance": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"is_illegal": map[string]any{
							"type":        "string",
							"enum":        []string{"Yes", "No"},
							"description": "Whether it contains illegal or non-compliant content, Yes indicates non-compliant, No indicates compliant",
						},
						"explanation": map[string]any{
							"type":        "string",
							"description": "Simplified Chinese explanation listing violated categories, exact violating locations, matched text, HTML or screenshot evidence, and violation reason",
						},
					},
					"required": []string{
						"is_illegal",
						"explanation",
					},
					"additionalProperties": false,
				},
			},
			"required": []string{
				"description",
				"keywords",
				"compliance",
			},
			"additionalProperties": false,
		},
	},
}
