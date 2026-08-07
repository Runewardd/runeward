package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConversationStreamPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		id   string
		ok   bool
	}{
		{"/v1/citadels/cit-1/conversation/stream", "cit-1", true},
		{"/v1/citadels/cit-1/conversation", "", false},
		{"/v1/citadels/a/b/conversation/stream", "", false},
	} {
		id, ok := conversationStreamSandboxID(tc.path)
		if id != tc.id || ok != tc.ok {
			t.Fatalf("%s: got (%q,%v), want (%q,%v)", tc.path, id, ok, tc.id, tc.ok)
		}
	}
}

func TestConversationTicketIsScopedAndSingleUse(t *testing.T) {
	s := &Server{}
	ticket, _, err := s.issueTicket(ticketScope{Kind: ticketKindConversation, SandboxID: "cit-1"}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/citadels/cit-1/conversation/stream?ticket="+ticket, nil)
	if _, ok, attempted := s.consumeRequestTicket(req); !ok || !attempted {
		t.Fatalf("first use ok=%v attempted=%v", ok, attempted)
	}
	if _, ok, attempted := s.consumeRequestTicket(req); ok || !attempted {
		t.Fatalf("second use ok=%v attempted=%v", ok, attempted)
	}
}

func TestConversationRoutesHideUnknownCitadel(t *testing.T) {
	h := newTestServer(t)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/citadels/missing/conversation"},
		{http.MethodPost, "/v1/citadels/missing/conversation"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.method == http.MethodPost {
			req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"role":"assistant","content":"hello"}`))
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if tc.method == http.MethodGet && rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status=%d want=404", tc.method, tc.path, rr.Code)
		}
		if tc.method == http.MethodPost && rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status=%d want=404", tc.method, tc.path, rr.Code)
		}
	}
}
