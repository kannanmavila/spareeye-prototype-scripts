// Re-run conversation analysis against a stored Firestore conversation (read-only).
// Useful for A/B testing analysis system prompts without recreating the call.
//
// Does NOT write back to Firestore.
//
// Usage (from Prototype-Backend so module deps resolve):
//
//	cd Prototype-Backend
//	go run ../Random-Scripts/reanalyze_conversation.go \
//	  -session 27e10308-237b-467b-99c8-665ed09b2a42
//
// Prompt variants:
//
//	go run ../Random-Scripts/reanalyze_conversation.go -session … -runs 3
//	go run ../Random-Scripts/reanalyze_conversation.go -session … -system-file ./prompt.txt
//	go run ../Random-Scripts/reanalyze_conversation.go -session … -system 'Custom prompt…'
//
// Requires GOOGLE_CLOUD_PROJECT and Application Default Credentials (Vertex, same as prod).
//
//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/genai"
)

const (
	analysisModel          = "gemini-3.1-flash-lite"
	vertexLocation         = "us-central1"
	analysisSummaryKey     = "summary"
	draftFieldPrefix       = "__draft__"
	defaultSystemPrompt    = "You are analyzing a conversation. The language may or may not be English. Read the transcript and fill in every field in the response schema using each field's description. Be factual and concise. The summary field is always required and must be a non-empty string. For all other fields, use JSON null when the transcript does not clearly support a value — do not invent or guess. Prefer null over weak inference. Never use the string \"null\"."
	phoneSystemPromptExtra = " This conversation took place over the phone (voice call)."
)

type transcriptToolCall struct {
	Name string `firestore:"name" json:"name"`
}

type transcriptEntry struct {
	Role      string               `firestore:"role" json:"role"`
	Text      string               `firestore:"text" json:"text"`
	Timestamp time.Time            `firestore:"timestamp" json:"timestamp"`
	ToolCalls []transcriptToolCall `firestore:"toolCalls,omitempty" json:"toolCalls,omitempty"`
}

type phoneCallInfo struct {
	CallerNumber string `firestore:"callerNumber" json:"callerNumber"`
}

type conversationAnalysis struct {
	Status         string         `firestore:"status"`
	Result         map[string]any `firestore:"result,omitempty"`
	SchemaSnapshot map[string]any `firestore:"schemaSnapshot,omitempty"`
}

type conversation struct {
	SessionID  string                `firestore:"sessionId"`
	SurfaceID  string                `firestore:"surfaceId"`
	Channel    string                `firestore:"channel"`
	PhoneCall  *phoneCallInfo        `firestore:"phoneCall,omitempty"`
	Transcript []transcriptEntry     `firestore:"transcript"`
	Analysis   *conversationAnalysis `firestore:"analysis,omitempty"`
}

func main() {
	sessionID := flag.String("session", "27e10308-237b-467b-99c8-665ed09b2a42", "Firestore conversation session ID")
	runs := flag.Int("runs", 1, "number of model calls (same inputs)")
	systemOverride := flag.String("system", "", "override system prompt (exact production prompt used if empty)")
	systemFile := flag.String("system-file", "", "read system prompt override from file")
	dumpTranscript := flag.Bool("dump-transcript", false, "print the formatted transcript sent to the model")
	dumpSchema := flag.Bool("dump-schema", false, "print the response schema sent to the model")
	noPhoneExtra := flag.Bool("no-phone-extra", false, "skip appending the phone-channel sentence even if channel=phone")
	flag.Parse()

	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT is required")
	}

	ctx := context.Background()
	fs, err := firestore.NewClient(ctx, project)
	if err != nil {
		log.Fatalf("firestore: %v", err)
	}
	defer fs.Close()

	doc, err := fs.Collection("conversations").Doc(*sessionID).Get(ctx)
	if err != nil {
		log.Fatalf("load conversation %s: %v", *sessionID, err)
	}
	var conv conversation
	if err := doc.DataTo(&conv); err != nil {
		log.Fatalf("parse conversation: %v", err)
	}
	if conv.Analysis == nil {
		log.Fatal("conversation has no analysis; cannot match production schema")
	}

	schema := ensureRequiredNullable(conv.Analysis.SchemaSnapshot)
	if schema == nil || len(schema) == 0 {
		log.Fatal("conversation has no analysis.schemaSnapshot; cannot match production schema")
	}

	transcriptText := formatTranscriptForAnalysis(conv.Transcript)
	if transcriptText == "" {
		log.Fatal("empty transcript")
	}

	systemInstruction := defaultSystemPrompt
	if strings.TrimSpace(*systemFile) != "" {
		b, err := os.ReadFile(*systemFile)
		if err != nil {
			log.Fatalf("read system-file: %v", err)
		}
		systemInstruction = strings.TrimSpace(string(b))
	} else if strings.TrimSpace(*systemOverride) != "" {
		systemInstruction = strings.TrimSpace(*systemOverride)
	}
	if !*noPhoneExtra && strings.EqualFold(strings.TrimSpace(conv.Channel), "phone") {
		if !strings.Contains(systemInstruction, phoneSystemPromptExtra) {
			systemInstruction += phoneSystemPromptExtra
		}
	}

	genaiSchema, err := jsonSchemaToGenaiSchema(schema)
	if err != nil {
		log.Fatalf("schema: %v", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: vertexLocation,
	})
	if err != nil {
		log.Fatalf("genai client: %v", err)
	}

	fmt.Printf("session:   %s\n", conv.SessionID)
	fmt.Printf("surface:   %s\n", conv.SurfaceID)
	fmt.Printf("channel:   %s\n", conv.Channel)
	fmt.Printf("model:     %s\n", analysisModel)
	fmt.Printf("runs:      %d\n", *runs)
	fmt.Printf("stored location in original analysis: %v\n", fieldOrMissing(conv.Analysis.Result, "location"))
	fmt.Printf("schema required: %v\n", schema["required"])
	fmt.Println("--- system prompt ---")
	fmt.Println(systemInstruction)
	fmt.Println("--- end system prompt ---")
	if *dumpSchema {
		raw, _ := json.MarshalIndent(schema, "", "  ")
		fmt.Println("--- schema ---")
		fmt.Println(string(raw))
		fmt.Println("--- end schema ---")
	}
	if *dumpTranscript {
		fmt.Println("--- transcript ---")
		fmt.Println(transcriptText)
		fmt.Println("--- end transcript ---")
	}

	type runStat struct {
		location any
		failed   bool
	}
	stats := make([]runStat, 0, *runs)
	locationCounts := map[string]int{}

	for i := 1; i <= *runs; i++ {
		fmt.Printf("\n========== run %d/%d ==========\n", i, *runs)
		result, err := structuredAnalysisCall(ctx, client, systemInstruction, transcriptText, genaiSchema)
		if err != nil {
			log.Printf("run %d failed: %v", i, err)
			stats = append(stats, runStat{failed: true})
			locationCounts["(failed)"]++
			continue
		}
		applyPhoneOverrides(&conv, result)
		pretty, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(pretty))
		loc, ok := result["location"]
		label := locationLabel(loc, ok)
		fmt.Printf(">>> location: %s\n", label)
		stats = append(stats, runStat{location: loc})
		locationCounts[label]++
	}

	fmt.Println("\n========== summary ==========")
	fmt.Printf("total runs: %d\n", *runs)
	keys := make([]string, 0, len(locationCounts))
	for k := range locationCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("location %q: %d/%d\n", k, locationCounts[k], *runs)
	}
}

func locationLabel(loc any, ok bool) string {
	if !ok || loc == nil {
		return "(null/missing)"
	}
	s := strings.TrimSpace(fmt.Sprint(loc))
	if s == "" {
		return "(empty)"
	}
	return s
}

func fieldOrMissing(m map[string]any, key string) string {
	if m == nil {
		return "(no stored result)"
	}
	v, ok := m[key]
	if !ok || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
		return "(missing)"
	}
	return fmt.Sprint(v)
}

func formatTranscriptForAnalysis(transcript []transcriptEntry) string {
	var b strings.Builder
	for _, entry := range transcript {
		role := entry.Role
		if role == "" {
			role = "unknown"
		}
		line := fmt.Sprintf("[%s] %s: %s", entry.Timestamp.UTC().Format(time.RFC3339), role, entry.Text)
		b.WriteString(line)
		b.WriteByte('\n')
		for _, tc := range entry.ToolCalls {
			b.WriteString(fmt.Sprintf("  (tool %s)\n", tc.Name))
		}
	}
	return strings.TrimSpace(b.String())
}

func structuredAnalysisCall(ctx context.Context, client *genai.Client, systemInstruction, userContent string, schema *genai.Schema) (map[string]any, error) {
	chat, err := client.Chats.Create(ctx, analysisModel, &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: systemInstruction}}},
		ResponseMIMEType:  "application/json",
		ResponseSchema:    schema,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("create chat: %w", err)
	}
	result, err := chat.SendMessage(ctx, genai.Part{Text: userContent})
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	raw := strings.TrimSpace(result.Text())
	var dest map[string]any
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		return nil, fmt.Errorf("parse structured response: %w", err)
	}
	return dest, nil
}

func applyPhoneOverrides(conv *conversation, result map[string]any) {
	if result == nil || conv == nil || !strings.EqualFold(conv.Channel, "phone") || conv.PhoneCall == nil {
		return
	}
	caller := strings.TrimSpace(strings.TrimPrefix(conv.PhoneCall.CallerNumber, "+"))
	if caller == "" {
		return
	}
	result["phone_number"] = caller
}

// ensureRequiredNullable mirrors Prototype-Backend ensureSummaryInSchema:
// every real field is required; user fields are nullable; summary is not.
func ensureRequiredNullable(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	raw, _ := json.Marshal(schema)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return nil
	}
	if strings.TrimSpace(stringFromAny(out["type"])) != "object" {
		out["type"] = "object"
	}
	props, ok := asJSONMap(out["properties"])
	if !ok || props == nil {
		props = map[string]any{}
	}
	if _, ok := props[analysisSummaryKey]; !ok {
		props[analysisSummaryKey] = map[string]any{
			"type":        "string",
			"description": "A concise summary of the conversation, including the customer's main intent and outcome.",
		}
	}
	if summaryProp, ok := asJSONMap(props[analysisSummaryKey]); ok {
		delete(summaryProp, "nullable")
		props[analysisSummaryKey] = summaryProp
	}

	userKeys := make([]string, 0, len(props))
	for key, rawProp := range props {
		if key == analysisSummaryKey || strings.HasPrefix(key, draftFieldPrefix) {
			continue
		}
		if prop, ok := asJSONMap(rawProp); ok {
			prop["nullable"] = true
			props[key] = prop
		}
		userKeys = append(userKeys, key)
	}
	sort.Strings(userKeys)
	required := append([]string{analysisSummaryKey}, userKeys...)
	out["properties"] = props
	out["required"] = anySliceFromStrings(required)
	return out
}

func jsonSchemaToGenaiSchema(schema map[string]any) (*genai.Schema, error) {
	props, ok := asJSONMap(schema["properties"])
	if !ok {
		return nil, fmt.Errorf("schema properties missing")
	}
	outProps := make(map[string]*genai.Schema, len(props))
	for key, rawProp := range props {
		prop, ok := asJSONMap(rawProp)
		if !ok {
			return nil, fmt.Errorf("property %q is invalid", key)
		}
		genProp, err := analysisPropertyToGenaiSchema(prop)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", key, err)
		}
		outProps[key] = genProp
	}
	return &genai.Schema{
		Type:       genai.TypeObject,
		Properties: outProps,
		Required:   stringSliceFromAny(schema["required"]),
	}, nil
}

func analysisPropertyToGenaiSchema(prop map[string]any) (*genai.Schema, error) {
	propType := strings.TrimSpace(stringFromAny(prop["type"]))
	out := &genai.Schema{Description: strings.TrimSpace(stringFromAny(prop["description"]))}
	if nullable, ok := prop["nullable"].(bool); ok && nullable {
		out.Nullable = genai.Ptr(true)
	}
	switch propType {
	case "string":
		out.Type = genai.TypeString
		if enumVals := stringSliceFromAny(prop["enum"]); len(enumVals) > 0 {
			out.Enum = enumVals
		}
	case "number":
		out.Type = genai.TypeNumber
	case "boolean":
		out.Type = genai.TypeBoolean
	case "array":
		out.Type = genai.TypeArray
		out.Items = &genai.Schema{Type: genai.TypeString}
	default:
		return nil, fmt.Errorf("unsupported type %q", propType)
	}
	return out, nil
}

func asJSONMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	return nil, false
}

func stringFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}

func stringSliceFromAny(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s := strings.TrimSpace(stringFromAny(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func anySliceFromStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
