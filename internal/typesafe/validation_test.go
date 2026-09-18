package typesafe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEvaluateRejectsInvalidResponsesWithoutRetry(t *testing.T) {
	for name, test := range map[string]struct {
		body string
		code string
	}{
		"model":       {body: `{"model":"jev-other","answers":{"q":{"type":"noul","noul":0.1}}}`},
		"cardinality": {body: `{"model":"jev-1.13.0","answers":{}}`},
		"key":         {body: `{"model":"jev-1.13.0","answers":{"other":{"type":"noul","noul":0.1}}}`, code: "q"},
		"type":        {body: `{"model":"jev-1.13.0","answers":{"q":{"type":"score","noul":0.1}}}`, code: "q"},
		"range low":   {body: `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":-0.1}}}`, code: "q"},
		"range high":  {body: `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":1.1}}}`, code: "q"},
	} {
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-typesafe-request-id", "request-validation")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "test-key", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Evaluate(context.Background(), "jev-1.13.0", "source", map[string]Question{"q": {Type: "noul", Instructions: "true"}})
			if err == nil || attempts.Load() != 1 || HTTPStatus(err) != http.StatusOK || RequestID(err) != "request-validation" || RuleCode(err) != test.code {
				t.Fatalf("attempts=%d request_id=%q status=%d code=%q error=%v", attempts.Load(), RequestID(err), HTTPStatus(err), RuleCode(err), err)
			}
		})
	}
}
