// Copyright 2026 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A2A protocol constants.
const (
	// Internal subject prefix for A2A (like mqttPrefix = "$MQTT.").
	a2aPrefix = "$A2A."

	// Agents publish to register/unregister/heartbeat.
	a2aAgentRegisterSubject   = a2aPrefix + "agent.register"
	a2aAgentUnregisterSubject = a2aPrefix + "agent.unregister"
	a2aAgentHeartbeatSubject  = a2aPrefix + "agent.heartbeat"

	// Default base path for the A2A HTTP endpoint.
	a2aDefaultBasePath = "/a2a"

	// Default request timeout for JSON-RPC calls.
	a2aDefaultRequestTimeout = 30 * time.Second

	// Default agent health window.
	a2aDefaultHealthWindow = 2 * time.Minute
)

// srvA2A holds server-level state for the A2A protocol.
type srvA2A struct {
	mu          sync.RWMutex
	listener    net.Listener
	listenerErr error
	server      *http.Server

	// Agent registry: tracks registered agents per account.
	agents a2aAgentManager
}

// a2aAgentManager tracks agents across all accounts.
type a2aAgentManager struct {
	mu     sync.RWMutex
	byAcct map[string]*a2aAccountAgentManager // key: account name
}

// a2aAccountAgentManager tracks agents within a single account.
type a2aAccountAgentManager struct {
	mu     sync.RWMutex
	agents map[string]*a2aRegisteredAgent // key: agent name
}

// a2aRegisteredAgent represents an agent registered with the server.
type a2aRegisteredAgent struct {
	card       A2AAgentCard
	registered time.Time
	lastSeen   time.Time
	taskCount  uint64 // atomic
}

// -------------------------------------------------------------------
// A2A Protocol Types (following Google A2A specification)
// -------------------------------------------------------------------

// A2AAgentCard describes an agent's identity and capabilities.
type A2AAgentCard struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Version            string            `json:"version"`
	Provider           *A2AProvider      `json:"provider,omitempty"`
	URL                string            `json:"url,omitempty"`
	Capabilities       A2ACapabilities   `json:"capabilities"`
	Skills             []A2ASkill        `json:"skills"`
	DefaultInputModes  []string          `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string          `json:"defaultOutputModes,omitempty"`
	NATSSubjects       *A2ANATSSubjects  `json:"natsSubjects,omitempty"`
}

// A2AProvider identifies the organization behind an agent.
type A2AProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

// A2ACapabilities describes what the agent supports.
type A2ACapabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

// A2ASkill describes a single capability an agent offers.
type A2ASkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// A2ANATSSubjects defines the NATS subjects an agent listens on.
type A2ANATSSubjects struct {
	MessageSend   string `json:"messageSend"`
	MessageStream string `json:"messageStream,omitempty"`
	TasksGet      string `json:"tasksGet,omitempty"`
	TasksCancel   string `json:"tasksCancel,omitempty"`
}

// A2ATask represents a unit of work in the A2A protocol.
type A2ATask struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    A2ATaskStatus  `json:"status"`
	Artifacts []A2AArtifact  `json:"artifacts,omitempty"`
	History   []A2ATaskStatus `json:"history,omitempty"`
	Kind      string         `json:"kind"` // "task"
}

// A2ATaskState defines the lifecycle states of a task.
type A2ATaskState string

const (
	A2ATaskStateSubmitted     A2ATaskState = "submitted"
	A2ATaskStateWorking       A2ATaskState = "working"
	A2ATaskStateInputRequired A2ATaskState = "input-required"
	A2ATaskStateCompleted     A2ATaskState = "completed"
	A2ATaskStateFailed        A2ATaskState = "failed"
	A2ATaskStateCanceled      A2ATaskState = "canceled"
	A2ATaskStateRejected      A2ATaskState = "rejected"
)

// A2ATaskStatus represents the current status of a task.
type A2ATaskStatus struct {
	State     A2ATaskState `json:"state"`
	Message   *A2AMessage  `json:"message,omitempty"`
	Timestamp time.Time    `json:"timestamp,omitempty"`
}

// A2AMessage represents a communication turn between client and agent.
type A2AMessage struct {
	Role      string    `json:"role"` // "user" or "agent"
	Parts     []A2APart `json:"parts"`
	MessageID string    `json:"messageId"`
	ContextID string    `json:"contextId,omitempty"`
	Kind      string    `json:"kind,omitempty"` // "message"
}

// A2APart is the smallest unit of content.
type A2APart struct {
	Kind string `json:"kind"` // "text", "data", "file"
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

// A2AArtifact is an output generated by an agent.
type A2AArtifact struct {
	ArtifactID string    `json:"artifactId,omitempty"`
	Name       string    `json:"name,omitempty"`
	Parts      []A2APart `json:"parts"`
}

// A2ATaskStatusUpdateEvent is published on a2a.tasks.<taskId>.status.
type A2ATaskStatusUpdateEvent struct {
	TaskID    string       `json:"taskId"`
	ContextID string      `json:"contextId"`
	Kind      string      `json:"kind"` // "status-update"
	Status    A2ATaskStatus `json:"status"`
	Final     bool         `json:"final"`
}

// -------------------------------------------------------------------
// JSON-RPC 2.0 Types
// -------------------------------------------------------------------

type a2aJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type a2aJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *a2aJSONRPCError `json:"error,omitempty"`
}

type a2aJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard A2A/JSON-RPC error codes.
const (
	a2aErrParseError     = -32700
	a2aErrInvalidRequest = -32600
	a2aErrMethodNotFound = -32601
	a2aErrInvalidParams  = -32602
	a2aErrInternalError  = -32603
	a2aErrTaskNotFound   = -32000
)

// a2aMessageSendParams is the params for message/send and message/stream.
type a2aMessageSendParams struct {
	Message A2AMessage `json:"message"`
}

// a2aTasksGetParams is the params for tasks/get.
type a2aTasksGetParams struct {
	TaskID string `json:"id"`
}

// a2aTasksCancelParams is the params for tasks/cancel.
type a2aTasksCancelParams struct {
	TaskID string `json:"id"`
}

// -------------------------------------------------------------------
// Server Startup
// -------------------------------------------------------------------

func (s *Server) startA2A() {
	if s.isShuttingDown() {
		return
	}

	sopts := s.getOpts()
	o := &sopts.A2A

	port := o.Port
	if port == -1 {
		port = 0
	}
	hp := net.JoinHostPort(o.Host, strconv.Itoa(port))

	var hl net.Listener
	var err error
	scheme := "http"

	if o.TLSConfig != nil {
		scheme = "https"
		hl, err = tls.Listen("tcp", hp, o.TLSConfig.Clone())
	} else {
		hl, err = natsListen("tcp", hp)
	}

	s.mu.Lock()
	s.a2a.listenerErr = err
	if err != nil {
		s.mu.Unlock()
		s.Fatalf("Unable to listen for A2A connections: %v", err)
		return
	}
	if port == 0 {
		o.Port = hl.Addr().(*net.TCPAddr).Port
	}
	s.a2a.listener = hl
	s.a2a.agents.byAcct = make(map[string]*a2aAccountAgentManager)
	s.mu.Unlock()

	s.Noticef("Listening for A2A clients on %s://%s", scheme,
		net.JoinHostPort(o.Host, strconv.Itoa(o.Port)))

	// Build the HTTP mux.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.a2aHandleWellKnown)
	mux.HandleFunc(a2aDefaultBasePath, s.a2aHandleJSONRPC)

	srv := &http.Server{
		Addr:              hp,
		Handler:           mux,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          log.New(&captureHTTPServerLog{s, "a2a: "}, _EMPTY_, 0),
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.a2a.server = srv
	s.mu.Unlock()

	// Start internal subscriptions for agent registration.
	s.a2aInitInternalSubs()

	go func() {
		if err := srv.Serve(hl); err != nil && err != http.ErrServerClosed {
			if !s.isShuttingDown() {
				s.Errorf("Error starting A2A listener: %v", err)
			}
		}
		srv.Close()
		s.done <- true
	}()
}

// -------------------------------------------------------------------
// Internal Subscriptions for Agent Registration
// -------------------------------------------------------------------

func (s *Server) a2aInitInternalSubs() {
	// Watch for agent registrations.
	if sa := s.SystemAccount(); sa != nil {
		sa.subscribeInternal(a2aAgentRegisterSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, rmsg []byte) {
			_, msg := c.msgParts(rmsg)
			var card A2AAgentCard
			if err := json.Unmarshal(msg, &card); err != nil {
				s.Warnf("A2A: invalid agent card in registration: %v", err)
				return
			}

			// Use the sender's account if possible, otherwise system account.
			regAcc := acc
			if regAcc == nil {
				regAcc = sa
			}

			s.a2aRegisterAgent(regAcc, &card)
			s.Noticef("A2A: Agent %q registered in account %q with %d skills",
				card.Name, regAcc.Name, len(card.Skills))

			if reply != _EMPTY_ {
				resp := []byte(`{"registered":true}`)
				s.sendInternalAccountMsg(regAcc, reply, resp)
			}
		})

		// Watch for unregistrations.
		sa.subscribeInternal(a2aAgentUnregisterSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, rmsg []byte) {
			_, msg := c.msgParts(rmsg)
			var unreg struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(msg, &unreg); err != nil {
				return
			}
			regAcc := acc
			if regAcc == nil {
				regAcc = sa
			}
			s.a2aUnregisterAgent(regAcc, unreg.Name)
			s.Noticef("A2A: Agent %q unregistered from account %q", unreg.Name, regAcc.Name)
		})

		// Watch for heartbeats.
		sa.subscribeInternal(a2aAgentHeartbeatSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, rmsg []byte) {
			_, msg := c.msgParts(rmsg)
			var hb struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(msg, &hb); err != nil {
				return
			}
			regAcc := acc
			if regAcc == nil {
				regAcc = sa
			}
			s.a2aUpdateAgentLastSeen(regAcc, hb.Name)
		})
	}
}

// -------------------------------------------------------------------
// Agent Registry
// -------------------------------------------------------------------

func (s *Server) a2aRegisterAgent(acc *Account, card *A2AAgentCard) {
	s.a2a.agents.mu.Lock()
	aam, ok := s.a2a.agents.byAcct[acc.Name]
	if !ok {
		aam = &a2aAccountAgentManager{
			agents: make(map[string]*a2aRegisteredAgent),
		}
		s.a2a.agents.byAcct[acc.Name] = aam
	}
	s.a2a.agents.mu.Unlock()

	now := time.Now()
	aam.mu.Lock()
	aam.agents[card.Name] = &a2aRegisteredAgent{
		card:       *card,
		registered: now,
		lastSeen:   now,
	}
	aam.mu.Unlock()
}

func (s *Server) a2aUnregisterAgent(acc *Account, name string) {
	s.a2a.agents.mu.RLock()
	aam, ok := s.a2a.agents.byAcct[acc.Name]
	s.a2a.agents.mu.RUnlock()
	if !ok {
		return
	}
	aam.mu.Lock()
	delete(aam.agents, name)
	aam.mu.Unlock()
}

func (s *Server) a2aUpdateAgentLastSeen(acc *Account, name string) {
	s.a2a.agents.mu.RLock()
	aam, ok := s.a2a.agents.byAcct[acc.Name]
	s.a2a.agents.mu.RUnlock()
	if !ok {
		return
	}
	aam.mu.Lock()
	if agent, ok := aam.agents[name]; ok {
		agent.lastSeen = time.Now()
	}
	aam.mu.Unlock()
}

func (s *Server) a2aGetAgentsForAccount(accName string) []*a2aRegisteredAgent {
	s.a2a.agents.mu.RLock()
	aam, ok := s.a2a.agents.byAcct[accName]
	s.a2a.agents.mu.RUnlock()
	if !ok {
		return nil
	}
	aam.mu.RLock()
	agents := make([]*a2aRegisteredAgent, 0, len(aam.agents))
	for _, a := range aam.agents {
		agents = append(agents, a)
	}
	aam.mu.RUnlock()
	return agents
}

// -------------------------------------------------------------------
// HTTP Handlers
// -------------------------------------------------------------------

// a2aHandleWellKnown serves GET /.well-known/agent.json.
// Returns agent cards for all registered agents.
func (s *Server) a2aHandleWellKnown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Collect all agents across all accounts.
	var cards []A2AAgentCard

	s.a2a.agents.mu.RLock()
	for _, aam := range s.a2a.agents.byAcct {
		aam.mu.RLock()
		for _, agent := range aam.agents {
			cards = append(cards, agent.card)
		}
		aam.mu.RUnlock()
	}
	s.a2a.agents.mu.RUnlock()

	// If a specific agent is requested via query param.
	if name := r.URL.Query().Get("agent"); name != _EMPTY_ {
		for _, card := range cards {
			if card.Name == name {
				data, _ := json.MarshalIndent(card, "", "  ")
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	if cards == nil {
		cards = []A2AAgentCard{}
	}
	data, _ := json.MarshalIndent(cards, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// a2aHandleJSONRPC is the main entry point for A2A JSON-RPC requests.
// POST /a2a dispatches based on the "method" field.
func (s *Server) a2aHandleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.a2aRespondError(w, nil, a2aErrParseError, "Failed to read request body")
		return
	}

	var req a2aJSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.a2aRespondError(w, nil, a2aErrParseError, "Invalid JSON")
		return
	}

	if req.JSONRPC != "2.0" {
		s.a2aRespondError(w, req.ID, a2aErrInvalidRequest, "Invalid JSON-RPC version")
		return
	}

	// Extract agent name from the params or query string.
	agentName := r.URL.Query().Get("agent")

	switch req.Method {
	case "message/send":
		s.a2aHandleMessageSend(w, agentName, &req)
	case "message/stream":
		s.a2aHandleMessageStream(w, r, agentName, &req)
	case "tasks/get":
		s.a2aHandleTasksGet(w, &req)
	case "tasks/cancel":
		s.a2aHandleTasksCancel(w, agentName, &req)
	default:
		s.a2aRespondError(w, req.ID, a2aErrMethodNotFound,
			fmt.Sprintf("Unknown method: %s", req.Method))
	}
}

// -------------------------------------------------------------------
// message/send — HTTP → NATS request-reply
// -------------------------------------------------------------------

func (s *Server) a2aHandleMessageSend(w http.ResponseWriter, agentName string, req *a2aJSONRPCRequest) {
	if agentName == _EMPTY_ {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Missing agent name (use ?agent=name)")
		return
	}

	var params a2aMessageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Invalid params")
		return
	}

	// Build the NATS subject for this agent.
	subject := fmt.Sprintf("a2a.agents.%s.message.send", agentName)

	timeout := a2aDefaultRequestTimeout

	// Use the system account's internal client to do a request-reply.
	sa := s.SystemAccount()
	if sa == nil {
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System account not available")
		return
	}

	// Build the payload as the full JSON-RPC request.
	payload, _ := json.Marshal(req)

	respCh := make(chan []byte, 1)
	reply := s.newRespInbox()

	s.mu.Lock()
	if s.sys == nil || s.sys.replies == nil {
		s.mu.Unlock()
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System not available")
		return
	}
	s.sys.replies[reply] = func(sub *subscription, _ *client, _ *Account, _, _ string, rmsg []byte) {
		_, msg := sub.client.msgParts(rmsg)
		// Copy the message since it may be reused.
		cp := make([]byte, len(msg))
		copy(cp, msg)
		select {
		case respCh <- cp:
		default:
		}
	}
	s.mu.Unlock()

	// Clean up reply handler when done.
	defer func() {
		s.mu.Lock()
		if s.sys != nil && s.sys.replies != nil {
			delete(s.sys.replies, reply)
		}
		s.mu.Unlock()
	}()

	// Publish the request with a reply subject.
	s.sendInternalAccountMsgWithReply(sa, subject, reply, nil, payload, false)

	// Wait for response or timeout.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-respCh:
		// Forward the agent's NATS response as the HTTP response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	case <-timer.C:
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "Agent did not respond (timeout)")
	}
}

// -------------------------------------------------------------------
// message/stream — HTTP → NATS subscribe + SSE
// -------------------------------------------------------------------

func (s *Server) a2aHandleMessageStream(w http.ResponseWriter, r *http.Request, agentName string, req *a2aJSONRPCRequest) {
	if agentName == _EMPTY_ {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Missing agent name (use ?agent=name)")
		return
	}

	var params a2aMessageSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Invalid params")
		return
	}

	// Send the initial request to the agent.
	subject := fmt.Sprintf("a2a.agents.%s.message.stream", agentName)
	sa := s.SystemAccount()
	if sa == nil {
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System account not available")
		return
	}

	payload, _ := json.Marshal(req)
	initRespCh := make(chan []byte, 1)
	reply := s.newRespInbox()

	s.mu.Lock()
	if s.sys == nil || s.sys.replies == nil {
		s.mu.Unlock()
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System not available")
		return
	}
	s.sys.replies[reply] = func(sub *subscription, _ *client, _ *Account, _, _ string, rmsg []byte) {
		_, msg := sub.client.msgParts(rmsg)
		cp := make([]byte, len(msg))
		copy(cp, msg)
		select {
		case initRespCh <- cp:
		default:
		}
	}
	s.mu.Unlock()

	s.sendInternalAccountMsgWithReply(sa, subject, reply, nil, payload, false)

	// Wait for initial response.
	timer := time.NewTimer(10 * time.Second)
	var initResp []byte
	select {
	case initResp = <-initRespCh:
		timer.Stop()
	case <-timer.C:
		s.mu.Lock()
		if s.sys != nil && s.sys.replies != nil {
			delete(s.sys.replies, reply)
		}
		s.mu.Unlock()
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "Agent did not respond (timeout)")
		return
	}

	s.mu.Lock()
	if s.sys != nil && s.sys.replies != nil {
		delete(s.sys.replies, reply)
	}
	s.mu.Unlock()

	// Parse response to get the task ID.
	var rpcResp a2aJSONRPCResponse
	if err := json.Unmarshal(initResp, &rpcResp); err != nil {
		s.a2aRespondError(w, req.ID, a2aErrParseError, "Invalid agent response")
		return
	}

	taskData, _ := json.Marshal(rpcResp.Result)
	var task A2ATask
	if err := json.Unmarshal(taskData, &task); err != nil || task.ID == _EMPTY_ {
		// No task ID, just return the response directly.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(initResp)
		return
	}

	// Set up SSE.
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send the initial task as the first SSE event.
	s.a2aWriteSSE(w, flusher, "status-update", initResp)

	// Subscribe to task events on NATS.
	eventSubject := fmt.Sprintf("a2a.tasks.%s.>", task.ID)

	eventCh := make(chan *a2aSSEEvent, 64)
	sub, err := sa.subscribeInternal(eventSubject, func(sub *subscription, c *client, acc *Account, subject, _ string, rmsg []byte) {
		_, msg := c.msgParts(rmsg)
		cp := make([]byte, len(msg))
		copy(cp, msg)

		eventType := "event"
		if strings.HasSuffix(subject, ".status") {
			eventType = "status-update"
		} else if strings.HasSuffix(subject, ".artifacts") {
			eventType = "artifact-update"
		}

		select {
		case eventCh <- &a2aSSEEvent{eventType: eventType, data: cp}:
		default:
			// Channel full, drop event.
		}
	})
	if err != nil {
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "Failed to subscribe to task events")
		return
	}
	defer sa.unsubscribeInternal(sub)

	// Bridge NATS events to SSE until task completes or client disconnects.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-eventCh:
			s.a2aWriteSSE(w, flusher, evt.eventType, evt.data)

			// Check if this is the final event.
			var statusEvt A2ATaskStatusUpdateEvent
			if err := json.Unmarshal(evt.data, &statusEvt); err == nil && statusEvt.Final {
				return
			}
		}
	}
}

type a2aSSEEvent struct {
	eventType string
	data      []byte
}

func (s *Server) a2aWriteSSE(w http.ResponseWriter, f http.Flusher, event string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	f.Flush()
}

// -------------------------------------------------------------------
// tasks/get
// -------------------------------------------------------------------

func (s *Server) a2aHandleTasksGet(w http.ResponseWriter, req *a2aJSONRPCRequest) {
	var params a2aTasksGetParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.TaskID == _EMPTY_ {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Missing or invalid task ID")
		return
	}

	// For now, tasks/get returns a not-found since we don't have JetStream
	// persistence wired up yet. Agents should use NATS subjects directly.
	s.a2aRespondError(w, req.ID, a2aErrTaskNotFound,
		fmt.Sprintf("Task %s not found (use NATS subjects for task state)", params.TaskID))
}

// -------------------------------------------------------------------
// tasks/cancel
// -------------------------------------------------------------------

func (s *Server) a2aHandleTasksCancel(w http.ResponseWriter, agentName string, req *a2aJSONRPCRequest) {
	if agentName == _EMPTY_ {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Missing agent name (use ?agent=name)")
		return
	}

	var params a2aTasksCancelParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.TaskID == _EMPTY_ {
		s.a2aRespondError(w, req.ID, a2aErrInvalidParams, "Missing or invalid task ID")
		return
	}

	// Forward to agent via NATS request-reply.
	subject := fmt.Sprintf("a2a.agents.%s.tasks.cancel", agentName)
	sa := s.SystemAccount()
	if sa == nil {
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System account not available")
		return
	}

	payload, _ := json.Marshal(req)
	respCh := make(chan []byte, 1)
	reply := s.newRespInbox()

	s.mu.Lock()
	if s.sys == nil || s.sys.replies == nil {
		s.mu.Unlock()
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "System not available")
		return
	}
	s.sys.replies[reply] = func(sub *subscription, _ *client, _ *Account, _, _ string, rmsg []byte) {
		_, msg := sub.client.msgParts(rmsg)
		cp := make([]byte, len(msg))
		copy(cp, msg)
		select {
		case respCh <- cp:
		default:
		}
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.sys != nil && s.sys.replies != nil {
			delete(s.sys.replies, reply)
		}
		s.mu.Unlock()
	}()

	s.sendInternalAccountMsgWithReply(sa, subject, reply, nil, payload, false)

	timer := time.NewTimer(a2aDefaultRequestTimeout)
	defer timer.Stop()

	select {
	case resp := <-respCh:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	case <-timer.C:
		s.a2aRespondError(w, req.ID, a2aErrInternalError, "Agent did not respond (timeout)")
	}
}

// -------------------------------------------------------------------
// JSON-RPC Response Helpers
// -------------------------------------------------------------------

func (s *Server) a2aRespondError(w http.ResponseWriter, id any, code int, message string) {
	resp := a2aJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &a2aJSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are still 200 per spec
	w.Write(data)
}

func (s *Server) a2aRespondResult(w http.ResponseWriter, id any, result any) {
	resp := a2aJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// -------------------------------------------------------------------
// Monitoring: /a2az
// -------------------------------------------------------------------

// A2Az is the monitoring data for the A2A protocol.
type A2Az struct {
	Agents     []A2AAgentInfoz `json:"agents"`
	TotalTasks uint64          `json:"total_tasks"`
	Port       int             `json:"port"`
}

// A2AAgentInfoz is agent info for the /a2az monitoring endpoint.
type A2AAgentInfoz struct {
	Name        string    `json:"name"`
	Account     string    `json:"account"`
	Skills      int       `json:"skills"`
	Registered  time.Time `json:"registered"`
	LastSeen    time.Time `json:"last_seen"`
	TaskCount   uint64    `json:"task_count"`
	Healthy     bool      `json:"healthy"`
}

const A2AzPath = "/a2az"

// HandleA2Az returns A2A agent monitoring data.
func (s *Server) HandleA2Az(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.httpReqStats[A2AzPath]++
	s.mu.Unlock()

	accountFilter := r.URL.Query().Get("acc")

	var agents []A2AAgentInfoz
	var totalTasks uint64

	s.a2a.agents.mu.RLock()
	for accName, aam := range s.a2a.agents.byAcct {
		if accountFilter != _EMPTY_ && accName != accountFilter {
			continue
		}
		aam.mu.RLock()
		for _, agent := range aam.agents {
			tc := atomic.LoadUint64(&agent.taskCount)
			totalTasks += tc
			agents = append(agents, A2AAgentInfoz{
				Name:       agent.card.Name,
				Account:    accName,
				Skills:     len(agent.card.Skills),
				Registered: agent.registered,
				LastSeen:   agent.lastSeen,
				TaskCount:  tc,
				Healthy:    time.Since(agent.lastSeen) < a2aDefaultHealthWindow,
			})
		}
		aam.mu.RUnlock()
	}
	s.a2a.agents.mu.RUnlock()

	if agents == nil {
		agents = []A2AAgentInfoz{}
	}

	result := &A2Az{
		Agents:     agents,
		TotalTasks: totalTasks,
		Port:       s.getOpts().A2A.Port,
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		s.Errorf("Error marshaling a2az response: %v", err)
		return
	}
	ResponseHandler(w, r, b)
}
