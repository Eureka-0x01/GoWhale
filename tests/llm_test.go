package tests

import (
	"encoding/json"
	"testing"

	"gowhale/internal/llm"
)

func TestParseXMLToolCalls_Single(t *testing.T) {
	content := `<tool_call>
{"name": "read_file", "arguments": {"path": "main.go"}}
</tool_call>`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("name = %q, want read_file", calls[0].Function.Name)
	}

	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args.Path != "main.go" {
		t.Errorf("path = %q, want main.go", args.Path)
	}
}

func TestParseXMLToolCalls_Multiple(t *testing.T) {
	content := `some text
<tool_call>
{"name": "read_file", "arguments": {"path": "main.go"}}
</tool_call>
middle
<tool_call>
{"name": "list_dir", "arguments": {"path": "."}}
</tool_call>
more`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("call[0] = %q, want read_file", calls[0].Function.Name)
	}
	if calls[1].Function.Name != "list_dir" {
		t.Errorf("call[1] = %q, want list_dir", calls[1].Function.Name)
	}
}

func TestParseXMLToolCalls_NoToolCalls(t *testing.T) {
	calls := llm.ParseXMLToolCalls("plain text, no tool calls")
	if len(calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(calls))
	}
}

func TestParseXMLToolCalls_InvalidJSON(t *testing.T) {
	content := `<tool_call>
{not valid json}
</tool_call>`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 0 {
		t.Errorf("expected 0 calls for invalid JSON, got %d", len(calls))
	}
}

func TestParseXMLToolCalls_NoNewlines(t *testing.T) {
	content := `<tool_call>{"name": "execute_shell", "arguments": {"command": "go build"}}</tool_call>`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "execute_shell" {
		t.Errorf("name = %q, want execute_shell", calls[0].Function.Name)
	}
}
func TestParseXMLToolCalls_FunctionEquals(t *testing.T) {
	content := `<function=list_dir> <parameter=path> . </parameter> </function>
<function=read_file> <parameter=paths> ["main.py","utils.py"] </parameter> </function>`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "list_dir" {
		t.Errorf("call[0].name = %q, want list_dir", calls[0].Function.Name)
	}
	if calls[1].Function.Name != "read_file" {
		t.Errorf("call[1].name = %q, want read_file", calls[1].Function.Name)
	}
}

func TestParseXMLToolCalls_FunctionEqualsSingleParam(t *testing.T) {
	content := `<function=web_search> <parameter=query> golang http handler context timeout </parameter> </function>`

	calls := llm.ParseXMLToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "web_search" {
		t.Errorf("name = %q, want web_search", calls[0].Function.Name)
	}
}

