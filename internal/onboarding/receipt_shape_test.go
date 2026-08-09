package onboarding

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDecodeReceiptStrictShape(t *testing.T) {
	request := receiptTestRequest(t)
	base := receiptJSON(t, preparedReceiptForTest(t, request))

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "unknown top-level field",
			data: func() []byte {
				object := receiptObject(t, base)
				object["unexpected"] = json.RawMessage(`true`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "unknown nested field",
			data: func() []byte {
				object := receiptObject(t, base)
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(object["worktree_proof"], &nested); err != nil {
					t.Fatalf("unmarshal worktree proof: %v", err)
				}
				nested["unexpected"] = json.RawMessage(`1`)
				object["worktree_proof"] = receiptObjectJSON(t, nested)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "missing required field",
			data: func() []byte {
				object := receiptObject(t, base)
				delete(object, "recovery")
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "null field",
			data: func() []byte {
				object := receiptObject(t, base)
				object["project_id"] = json.RawMessage(`null`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "wrong field type",
			data: func() []byte {
				object := receiptObject(t, base)
				object["schema_version"] = json.RawMessage(`"1"`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{name: "trailing JSON", data: append(append([]byte(nil), base...), []byte(`{}`)...)},
		{name: "invalid UTF-8", data: append(append([]byte(nil), base...), 0xff)},
		{
			name: "duplicate top-level field",
			data: func() []byte {
				trimmed := bytes.TrimSpace(base)
				return append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(`,"operation_id":"22222222-2222-2222-2222-222222222222"}`)...)
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeReceipt(test.data); err == nil {
				t.Fatalf("DecodeReceipt accepted invalid %s", test.name)
			}
		})
	}
}

func TestDecodeReceiptRequiresBooleanPresence(t *testing.T) {
	request := receiptTestRequest(t)
	base := receiptJSON(t, preparedReceiptForTest(t, request))
	for _, field := range []struct {
		object string
		key    string
	}{
		{object: "worktree_proof", key: "clean"},
		{object: "session_proof", key: "required"},
	} {
		t.Run(field.object+"/"+field.key, func(t *testing.T) {
			object := receiptObject(t, base)
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(object[field.object], &nested); err != nil {
				t.Fatalf("unmarshal nested object: %v", err)
			}
			delete(nested, field.key)
			object[field.object] = receiptObjectJSON(t, nested)
			if _, err := DecodeReceipt(receiptObjectJSON(t, object)); err == nil {
				t.Fatalf("DecodeReceipt accepted missing %s.%s", field.object, field.key)
			}
		})
	}
}
