# A2A Agentic Support over NATS — Design Document

## Overview

This document describes how to add [A2A (Agent-to-Agent)](https://a2a-protocol.org) protocol support to NATS, enabling AI agents to discover and communicate with each other using NATS as the transport layer instead of HTTP.

The A2A protocol (by Google, now under the Linux Foundation) defines how independent AI agents — potentially built with different frameworks — can interoperate. It standardizes agent discovery, task lifecycle, streaming, and multi-modal content exchange. By mapping A2A onto NATS, we gain decoupled, scalable, multi-tenant agent communication with built-in clustering, persistence (JetStream), and fine-grained authorization.

---

## Why NATS for A2A?

The standard A2A protocol uses HTTP + JSON-RPC 2.0. While this works for point-to-point interactions, NATS provides significant advantages for agentic systems at scale:

| Concern | HTTP A2A | NATS A2A |
|---|---|---|
| **Discovery** | Well-known URL per agent | Subject-based; query `a2a.discovery.>` |
| **Routing** | Client must know agent URL | Subject-based; agents subscribe to skill subjects |
| **Load balancing** | External LB required | Native queue groups |
| **Streaming** | SSE (unidirectional) | NATS subjects (bidirectional) |
| **Persistence** | Not built in | JetStream streams for task durability |
| **Multi-tenancy** | Per-agent auth config | NATS accounts with import/export |
| **Fan-out** | N HTTP calls | Single publish, N subscribers |
| **Clustering** | Per-agent deployment | NATS cluster routes it |

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                      NATS Cluster                             │
│                                                               │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐    │
│  │  Agent A     │     │  Agent B     │     │  Agent C     │   │
│  │  (Client)    │     │  (Server)    │     │  (Server)    │   │
│  │              │     │              │     │              │   │
│  │  Publishes   │     │  Subscribes  │     │  Subscribes  │   │
│  │  to a2a.*    │────▶│  a2a.agents  │     │  a2a.agents  │   │
│  │              │     │  .recipe.*   │     │  .travel.*   │   │
│  └─────────────┘     └─────────────┘     └─────────────┘    │
│                                                               │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │                    JetStream                             │ │
│  │  ┌───────────────┐  ┌───────────────┐                   │ │
│  │  │ A2A_TASKS     │  │ A2A_EVENTS    │                   │ │
│  │  │ (task state)  │  │ (status/artifact│                 │ │
│  │  │               │  │  updates)      │                  │ │
│  │  └───────────────┘  └───────────────┘                   │ │
│  └─────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

---

## Subject Namespace Design

All A2A interactions use a structured subject hierarchy:

```
a2a.discovery.>                        # Agent discovery
a2a.discovery.<agent-name>             # Specific agent card request
a2a.discovery._broadcast               # Broadcast discovery query

a2a.agents.<agent-name>.message.send   # message/send (synchronous request-reply)
a2a.agents.<agent-name>.message.stream # message/stream (streaming via subjects)
a2a.agents.<agent-name>.tasks.get      # tasks/get
a2a.agents.<agent-name>.tasks.cancel   # tasks/cancel

a2a.tasks.<task-id>.status             # Task status updates (pub/sub)
a2a.tasks.<task-id>.artifacts          # Task artifact updates (pub/sub)
a2a.tasks.<task-id>.input              # Input for input-required state

a2a.events.<context-id>.>             # All events for a conversation context
```

### Subject Rationale

- **`a2a.discovery.>`** — Enables both targeted and broadcast agent discovery. An agent subscribes to `a2a.discovery.<its-name>` and optionally `a2a.discovery._broadcast` to respond to "find all agents" queries.
- **`a2a.agents.<name>.*`** — Maps directly to A2A JSON-RPC methods. Uses NATS request-reply for synchronous calls.
- **`a2a.tasks.<task-id>.*`** — Per-task event channels. Clients subscribe to receive streaming updates, replacing SSE.
- **`a2a.events.<context-id>.>`** — Groups all events in a conversational context, useful for multi-turn agent interactions.

---

## Mapping A2A Concepts to NATS

### 1. Agent Card & Discovery

Instead of `/.well-known/agent.json`, agents register by subscribing and responding:

```go
// Agent server subscribes to its discovery subject
nc.Subscribe("a2a.discovery.recipe-agent", func(msg *nats.Msg) {
    card := AgentCard{
        Name:        "Recipe Agent",
        Description: "Finds and suggests recipes",
        Version:     "1.0.0",
        // URL is replaced by the NATS subject pattern
        Subjects: AgentSubjects{
            MessageSend:   "a2a.agents.recipe-agent.message.send",
            MessageStream: "a2a.agents.recipe-agent.message.stream",
            TasksGet:      "a2a.agents.recipe-agent.tasks.get",
            TasksCancel:   "a2a.agents.recipe-agent.tasks.cancel",
        },
        Capabilities: Capabilities{
            Streaming:             true,
            PushNotifications:     false,
            StateTransitionHistory: true,
        },
        Skills: []Skill{
            {
                ID:          "find-recipe",
                Name:        "Find Recipe",
                Description: "Finds recipes based on ingredients",
                InputModes:  []string{"text"},
                OutputModes: []string{"text"},
            },
        },
    }
    data, _ := json.Marshal(card)
    msg.Respond(data)
})

// Also respond to broadcast discovery
nc.Subscribe("a2a.discovery._broadcast", func(msg *nats.Msg) {
    // Same response
})
```

**Client discovering agents:**

```go
// Discover a specific agent
resp, err := nc.Request("a2a.discovery.recipe-agent", nil, 2*time.Second)

// Discover ALL agents (fan-out via broadcast)
inbox := nats.NewInbox()
sub, _ := nc.SubscribeSync(inbox)
nc.PublishRequest("a2a.discovery._broadcast", inbox, nil)
// Collect responses for a timeout window
for {
    msg, err := sub.NextMsg(1 * time.Second)
    if err != nil { break }
    var card AgentCard
    json.Unmarshal(msg.Data, &card)
    // Process discovered agent
}
```

### 2. message/send (Synchronous Request-Reply)

Maps directly to NATS request-reply:

```go
// Client sends a task
req := JSONRPCRequest{
    JSONRPC: "2.0",
    ID:      1,
    Method:  "message/send",
    Params: MessageSendParams{
        Message: Message{
            Role: "user",
            Parts: []Part{
                {Kind: "text", Text: "Find me a pasta carbonara recipe"},
            },
            MessageID: uuid.New().String(),
        },
    },
}
data, _ := json.Marshal(req)
resp, err := nc.Request("a2a.agents.recipe-agent.message.send", data, 30*time.Second)
// resp.Data contains JSON-RPC response with Task or Message
```

**Agent server side:**

```go
nc.QueueSubscribe("a2a.agents.recipe-agent.message.send", "recipe-workers", func(msg *nats.Msg) {
    var req JSONRPCRequest
    json.Unmarshal(msg.Data, &req)

    // Process the task...
    task := processTask(req.Params)

    resp := JSONRPCResponse{
        JSONRPC: "2.0",
        ID:      req.ID,
        Result:  task,
    }
    data, _ := json.Marshal(resp)
    msg.Respond(data)
})
```

Note the use of **QueueSubscribe** — this gives us automatic load balancing across multiple agent instances for free.

### 3. message/stream (Streaming via Subjects)

Instead of SSE, use a dedicated NATS subject for task updates:

```go
// Client initiates a streaming task
req := JSONRPCRequest{
    JSONRPC: "2.0",
    ID:      2,
    Method:  "message/stream",
    Params: MessageStreamParams{
        Message: Message{
            Role: "user",
            Parts: []Part{
                {Kind: "text", Text: "Write a detailed essay about pasta"},
            },
            MessageID: uuid.New().String(),
        },
    },
}
data, _ := json.Marshal(req)

// First, subscribe to the task's event subjects
taskID := uuid.New().String()
statusSub, _ := nc.Subscribe("a2a.tasks."+taskID+".status", func(msg *nats.Msg) {
    var event TaskStatusUpdateEvent
    json.Unmarshal(msg.Data, &event)
    fmt.Printf("Task %s: state=%s\n", taskID, event.Status.State)
    if event.Final {
        // Terminal state reached
    }
})
artifactSub, _ := nc.Subscribe("a2a.tasks."+taskID+".artifacts", func(msg *nats.Msg) {
    var event TaskArtifactUpdateEvent
    json.Unmarshal(msg.Data, &event)
    // Process artifact chunk
})

// Send the request — agent responds with initial Task containing the taskID
resp, err := nc.Request("a2a.agents.recipe-agent.message.stream", data, 5*time.Second)
// resp contains Task with ID, client is now receiving events on the subscriptions above
```

**Agent server side (publishing stream events):**

```go
func handleStreamingTask(msg *nats.Msg, nc *nats.Conn) {
    var req JSONRPCRequest
    json.Unmarshal(msg.Data, &req)

    taskID := uuid.New().String()

    // Respond with initial task
    task := Task{
        ID:        taskID,
        ContextID: uuid.New().String(),
        Status:    TaskStatus{State: "submitted"},
        Kind:      "task",
    }
    resp, _ := json.Marshal(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
    msg.Respond(resp)

    // Now stream updates via the task subject
    go func() {
        // Working state
        publishStatus(nc, taskID, "working", false)

        // Stream artifact chunks
        for i, chunk := range generateChunks() {
            event := TaskArtifactUpdateEvent{
                TaskID:  taskID,
                Kind:    "artifact-update",
                Artifact: Artifact{Parts: []Part{{Kind: "text", Text: chunk}}},
                Append:   i > 0,
                LastChunk: i == len(chunks)-1,
            }
            data, _ := json.Marshal(event)
            nc.Publish("a2a.tasks."+taskID+".artifacts", data)
        }

        // Completed
        publishStatus(nc, taskID, "completed", true)
    }()
}
```

### 4. Task Persistence with JetStream

For durable task tracking, use JetStream:

```go
// Create streams for A2A task management
js, _ := nc.JetStream()

// Task state stream — stores the latest state of each task
js.AddStream(&nats.StreamConfig{
    Name:      "A2A_TASKS",
    Subjects:  []string{"a2a.tasks.*.status"},
    Storage:   nats.FileStorage,
    Retention: nats.WorkQueuePolicy,
    MaxAge:    24 * time.Hour, // Retain tasks for 24h
})

// Task events stream — full history of events
js.AddStream(&nats.StreamConfig{
    Name:      "A2A_EVENTS",
    Subjects:  []string{"a2a.tasks.*.>", "a2a.events.*.>"},
    Storage:   nats.FileStorage,
    Retention: nats.LimitsPolicy,
    MaxAge:    7 * 24 * time.Hour, // 7-day history
})
```

This enables:
- **tasks/get** — Retrieve task state from the `A2A_TASKS` stream
- **tasks/resubscribe** — Replay missed events from `A2A_EVENTS` stream via a consumer
- **Audit trails** — Full history of all agent interactions
- **Crash recovery** — Agents can resume in-progress tasks after restart

### 5. Multi-Tenancy via NATS Accounts

NATS accounts provide natural isolation for multi-tenant agent deployments:

```
# nats-server.conf

accounts {
  COMPANY_A {
    users: [{user: agent_a, password: secret}]
    exports: [
      {service: "a2a.agents.recipe-agent.>"}      # Export agent as a service
      {stream: "a2a.discovery.recipe-agent"}       # Export discovery
    ]
  }

  COMPANY_B {
    users: [{user: client_b, password: secret}]
    imports: [
      {service: {account: COMPANY_A, subject: "a2a.agents.recipe-agent.>"}}
      {stream: {account: COMPANY_A, subject: "a2a.discovery.recipe-agent"}}
    ]
  }
}
```

This means:
- Agents in `COMPANY_A` are isolated from `COMPANY_B`
- Cross-account agent access is explicitly granted via imports/exports
- Per-account JetStream limits control resource usage

### 6. Authorization (Subject-Level Permissions)

Fine-grained access control on who can call which agents:

```
authorization {
  users: [
    {
      user: "agent-caller"
      permissions: {
        publish: {
          allow: [
            "a2a.agents.recipe-agent.message.send",
            "a2a.discovery.>"
          ]
          deny: [
            "a2a.agents.*.tasks.cancel"  # Cannot cancel other agents' tasks
          ]
        }
        subscribe: {
          allow: ["a2a.tasks.>", "_INBOX.>"]
        }
      }
    }
  ]
}
```

---

## Data Types (Go)

Core types for the NATS A2A implementation:

```go
package a2a

import "time"

// AgentCard describes an agent's identity and capabilities (replaces .well-known/agent.json).
type AgentCard struct {
    Name            string          `json:"name"`
    Description     string          `json:"description,omitempty"`
    Version         string          `json:"version"`
    Provider        *Provider       `json:"provider,omitempty"`
    Subjects        AgentSubjects   `json:"subjects"`
    Capabilities    Capabilities    `json:"capabilities"`
    Skills          []Skill         `json:"skills"`
    DefaultInputModes  []string     `json:"defaultInputModes,omitempty"`
    DefaultOutputModes []string     `json:"defaultOutputModes,omitempty"`
}

// AgentSubjects defines the NATS subjects an agent listens on (replaces URL).
type AgentSubjects struct {
    MessageSend   string `json:"messageSend"`
    MessageStream string `json:"messageStream,omitempty"`
    TasksGet      string `json:"tasksGet,omitempty"`
    TasksCancel   string `json:"tasksCancel,omitempty"`
}

type Provider struct {
    Organization string `json:"organization"`
    URL          string `json:"url,omitempty"`
}

type Capabilities struct {
    Streaming              bool `json:"streaming"`
    PushNotifications      bool `json:"pushNotifications"`
    StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type Skill struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    InputModes  []string `json:"inputModes,omitempty"`
    OutputModes []string `json:"outputModes,omitempty"`
}

// Task represents a unit of work in the A2A protocol.
type Task struct {
    ID        string       `json:"id"`
    ContextID string       `json:"contextId"`
    Status    TaskStatus   `json:"status"`
    Artifacts []Artifact   `json:"artifacts,omitempty"`
    History   []TaskStatus `json:"history,omitempty"`
    Kind      string       `json:"kind"` // always "task"
}

// TaskState defines the lifecycle states of a task.
type TaskState string

const (
    TaskStateSubmitted     TaskState = "submitted"
    TaskStateWorking       TaskState = "working"
    TaskStateInputRequired TaskState = "input-required"
    TaskStateCompleted     TaskState = "completed"
    TaskStateFailed        TaskState = "failed"
    TaskStateCanceled      TaskState = "canceled"
    TaskStateRejected      TaskState = "rejected"
)

type TaskStatus struct {
    State     TaskState `json:"state"`
    Message   *Message  `json:"message,omitempty"`
    Timestamp time.Time `json:"timestamp,omitempty"`
}

// Message represents a communication turn between client and agent.
type Message struct {
    Role      string `json:"role"` // "user" or "agent"
    Parts     []Part `json:"parts"`
    MessageID string `json:"messageId"`
    ContextID string `json:"contextId,omitempty"`
    Kind      string `json:"kind,omitempty"` // "message"
}

// Part is the smallest unit of content.
type Part struct {
    Kind string `json:"kind"` // "text", "data", "file", etc.
    Text string `json:"text,omitempty"`
    Data any    `json:"data,omitempty"`
    URI  string `json:"uri,omitempty"`
}

// Artifact is an output generated by an agent.
type Artifact struct {
    ArtifactID string `json:"artifactId"`
    Name       string `json:"name,omitempty"`
    Parts      []Part `json:"parts"`
}

// TaskStatusUpdateEvent is published on a2a.tasks.<taskId>.status
type TaskStatusUpdateEvent struct {
    TaskID    string     `json:"taskId"`
    ContextID string    `json:"contextId"`
    Kind      string    `json:"kind"` // "status-update"
    Status    TaskStatus `json:"status"`
    Final     bool       `json:"final"`
}

// TaskArtifactUpdateEvent is published on a2a.tasks.<taskId>.artifacts
type TaskArtifactUpdateEvent struct {
    TaskID    string   `json:"taskId"`
    ContextID string   `json:"contextId"`
    Kind      string   `json:"kind"` // "artifact-update"
    Artifact  Artifact `json:"artifact"`
    Append    bool     `json:"append"`
    LastChunk bool     `json:"lastChunk"`
}

// JSON-RPC 2.0 envelope types

type JSONRPCRequest struct {
    JSONRPC string `json:"jsonrpc"`
    ID      any    `json:"id"`
    Method  string `json:"method"`
    Params  any    `json:"params"`
}

type JSONRPCResponse struct {
    JSONRPC string        `json:"jsonrpc"`
    ID      any           `json:"id"`
    Result  any           `json:"result,omitempty"`
    Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

// Standard A2A error codes
const (
    ErrParseError       = -32700
    ErrInvalidRequest   = -32600
    ErrMethodNotFound   = -32601
    ErrInvalidParams    = -32602
    ErrInternalError    = -32603
    ErrTaskNotFound     = -32000
    ErrTaskNotCancelable = -32001
    ErrPushNotSupported = -32002
    ErrUnsupported      = -32003
)
```

---

## Implementation Approaches

There are three approaches to integrating A2A with NATS, from least to most invasive:

### Approach 1: Client Library (Recommended Starting Point)

Build a Go client library (`nats-a2a-go`) that implements A2A semantics on top of the standard NATS client. **No changes to nats-server required.**

```
github.com/nats-io/nats-a2a-go/
├── agent.go          # AgentServer — handles incoming A2A requests
├── client.go         # AgentClient — sends A2A requests to agents
├── discovery.go      # Agent discovery via NATS subjects
├── types.go          # A2A data types (AgentCard, Task, Message, etc.)
├── stream.go         # Streaming support (subject-based SSE replacement)
├── jetstream.go      # Optional JetStream integration for persistence
└── examples/
    ├── recipe-agent/
    └── travel-agent/
```

**Pros**: No server changes, quick to ship, works with existing NATS deployments.
**Cons**: No server-side enforcement of A2A semantics.

### Approach 2: HTTP Gateway (A2A-to-NATS Bridge)

A standalone service that accepts standard HTTP A2A requests and translates them to NATS messages:

```
HTTP A2A Client ──▶ A2A-NATS Gateway ──▶ NATS ──▶ Agent (NATS subscriber)
                    (translates HTTP       │
                     to NATS subjects)     │
                                           ▼
                                     JetStream
                                   (task persistence)
```

This preserves compatibility with existing HTTP A2A clients while allowing agents to be built on NATS. The gateway:
- Serves `/.well-known/agent.json` by querying NATS discovery subjects
- Translates `message/send` HTTP POST → NATS request to `a2a.agents.<name>.message.send`
- Translates `message/stream` → NATS subscribe + SSE response
- Translates `tasks/get` → JetStream KV lookup or NATS request

### Approach 3: Server-Side Protocol Support

Add A2A as a first-class protocol in nats-server, similar to MQTT or WebSocket support. This would involve:

1. **New listener** in `server.go` for A2A HTTP endpoints
2. **Protocol handler** that maps A2A JSON-RPC to internal NATS operations
3. **Agent registry** backed by a KV store or dedicated stream
4. **Monitoring** endpoints at `/a2az` for agent metrics

This is the most ambitious approach and should only be pursued after validating the client library approach. See the detailed design below.

---

## Approach 3: First-Class Server-Side A2A — Detailed Design

This section describes what first-class A2A protocol support inside nats-server would look like, modeled after how MQTT (`server/mqtt.go`, ~5900 lines) and WebSocket (`server/websocket.go`, ~2000 lines) are integrated today.

### How NATS Integrates Protocols Today

NATS server follows a consistent pattern for each protocol it supports:

| Protocol | Integration Style | Key Mechanism |
|---|---|---|
| **MQTT** | Dedicated TCP listener, binary protocol parser, topic↔subject mapping, JetStream for QoS persistence | `srvMQTT` struct in `Server`, `startMQTT()`, `mqttParse()` dispatcher |
| **WebSocket** | HTTP listener, upgrade handshake, frames wrap raw NATS protocol | `srvWebsocket` struct in `Server`, `startWebsocketServer()`, `wsUpgrade()` |
| **HTTP Monitoring** | HTTP listener, `ServeMux` with `HandleFunc` per endpoint, JSON responses | `startMonitoring()`, `mux.HandleFunc(path, handler)` |

A2A would be a **hybrid** — it uses HTTP like monitoring/WebSocket, but has its own protocol semantics like MQTT. The closest analog is: **MQTT's protocol-translation approach + HTTP monitoring's listener infrastructure**.

### File Layout

Following the MQTT pattern (`server/mqtt.go`), the implementation would live in the `server/` package:

```
server/
├── a2a.go              # Core A2A types, protocol handler, JSON-RPC dispatch (~1500 lines)
├── a2a_agent_registry.go # Agent registration, discovery, health tracking (~500 lines)
├── a2a_tasks.go        # Task lifecycle, JetStream persistence (~800 lines)
├── a2a_test.go         # Tests
├── opts.go             # + A2AOpts struct (like MQTTOpts)
├── server.go           # + srvA2A field, startA2A() call
└── monitor.go          # + /a2az endpoint
```

### 1. Server Struct Integration

Following the pattern at `server/server.go:312-316` where MQTT and WebSocket are embedded:

```go
// In server/server.go — Server struct
type Server struct {
    // ... existing fields ...

    // Websocket structure
    websocket srvWebsocket

    // MQTT structure
    mqtt srvMQTT

    // A2A structure                          // <-- NEW
    a2a srvA2A                                // <-- NEW

    // ...
}
```

### 2. Server-Level A2A State (`server/a2a.go`)

Modeled after `srvMQTT` (`mqtt.go:246-251`) and `srvWebsocket` (`websocket.go:108-130`):

```go
// srvA2A holds server-level state for the A2A protocol.
// Stored as s.a2a in the Server struct.
type srvA2A struct {
    mu          sync.RWMutex
    listener    net.Listener
    listenerErr error
    server      *http.Server

    // Agent registry: tracks all registered agents per account.
    // Key is account name → per-account agent manager.
    // Follows mqttSessionManager pattern (mqtt.go:253-256).
    agents a2aAgentManager

    // Internal NATS client used for JetStream API calls,
    // following the mqttJSA pattern (mqtt.go:272-275).
    jsa a2aJSA
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
    jsa    a2aJSA                         // per-account JS client
}

// a2aRegisteredAgent represents an agent that has registered with the server.
type a2aRegisteredAgent struct {
    card       AgentCard
    registered time.Time
    lastSeen   time.Time
    taskCount  uint64 // atomic
}

// a2aJSA is the JetStream API abstraction for A2A.
// Modeled after mqttJSA (mqtt.go:279-290).
type a2aJSA struct {
    c       *client       // internal system client
    sendq   *ipQueue[*a2aJSARequest]
    replies sync.Map
    timeout time.Duration
}
```

### 3. Configuration (`server/opts.go`)

Following `MQTTOpts` (`opts.go:612-707`):

```go
// A2AOpts configures the A2A protocol listener.
type A2AOpts struct {
    // Host/Port for the A2A HTTP listener.
    Host string
    Port int

    // Authentication (override global auth for A2A clients).
    NoAuthUser string
    Username   string
    Password   string
    Token      string

    // JetStream domain for A2A task persistence.
    JsDomain string

    // Number of stream replicas for task state.
    StreamReplicas int

    // TLS configuration.
    TLSConfig *tls.Config
    TLSMap    bool
    TLSTimeout float64

    // Task configuration.
    TaskMaxAge    time.Duration // How long to retain completed tasks (default: 24h)
    TaskMaxBytes  int64         // Max total task storage per account

    // Request timeout for JSON-RPC calls (default: 30s)
    RequestTimeout time.Duration

    // Agent health check interval (default: 30s)
    AgentHealthInterval time.Duration

    // CORS settings for the A2A HTTP endpoint.
    AllowedOrigins []string

    // Base path for HTTP endpoints (default: "/a2a")
    BasePath string

    // Snapshot of configured TLS options.
    tlsConfigOpts *TLSConfigOpts
}
```

**Configuration file syntax:**

```
# nats-server.conf
a2a {
    host: "0.0.0.0"
    port: 8443

    tls {
        cert_file: "server.crt"
        key_file: "server.key"
    }

    # JetStream domain for task persistence
    js_domain: "a2a"

    # Task retention
    task_max_age: "24h"

    # CORS
    allowed_origins: ["https://app.example.com"]

    # Authentication (can be separate from main NATS auth)
    authorization {
        username: "a2a-admin"
        password: "secret"
    }
}
```

And the Options struct addition:

```go
// In server/opts.go — Options struct
type Options struct {
    // ...existing fields...
    MQTT      MQTTOpts
    A2A       A2AOpts   // <-- NEW
    Websocket WebsocketOpts
    // ...
}
```

### 4. Server Startup (`server/server.go`)

Following the pattern at `server.go:2513-2521` where MQTT and WebSocket are started:

```go
// In Server.Start(), after JetStream initialization:

// Start MQTT if configured.
if opts.MQTT.Port != 0 {
    s.startMQTT()
}

// Start A2A if configured.                    // <-- NEW
if opts.A2A.Port != 0 {                        // <-- NEW
    s.startA2A()                               // <-- NEW
}                                              // <-- NEW

// Start Websocket if configured.
if opts.Websocket.Port != 0 {
    s.startWebsocketServer()
}
```

### 5. A2A Listener & HTTP Handler (`server/a2a.go`)

Combines the HTTP listener pattern from `startMonitoring()` (`server.go:3090-3202`) with protocol-specific logic like `startMQTT()` (`mqtt.go:501-537`):

```go
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

    // Create listener (with optional TLS)
    var hl net.Listener
    var err error
    scheme := "http"

    if o.TLSConfig != nil {
        scheme = "https"
        hl, err = tls.Listen("tcp", hp, o.TLSConfig.Clone())
    } else {
        hl, err = natsListen("tcp", hp)
    }

    if err != nil {
        s.mu.Lock()
        s.a2a.listenerErr = err
        s.mu.Unlock()
        s.Fatalf("Unable to listen for A2A connections: %v", err)
        return
    }

    if port == 0 {
        o.Port = hl.Addr().(*net.TCPAddr).Port
    }

    s.Noticef("Starting A2A listener on %s://%s", scheme,
        net.JoinHostPort(o.Host, strconv.Itoa(o.Port)))

    // Initialize agent registry
    s.mu.Lock()
    s.a2a.listener = hl
    s.a2a.agents.byAcct = make(map[string]*a2aAccountAgentManager)
    s.mu.Unlock()

    // Create HTTP mux with A2A endpoints
    // Pattern from startMonitoring() (server.go:3137-3170)
    mux := http.NewServeMux()

    basePath := o.BasePath
    if basePath == "" {
        basePath = "/a2a"
    }

    // Standard A2A endpoints
    mux.HandleFunc("/.well-known/agent.json", s.a2aHandleWellKnown)
    mux.HandleFunc(basePath, s.a2aHandleJSONRPC)

    // HTTP server — pattern from startMonitoring() (server.go:3175-3181)
    srv := &http.Server{
        Addr:              hp,
        Handler:           mux,
        MaxHeaderBytes:    1 << 20,
        ErrorLog:          log.New(&captureHTTPServerLog{s, "a2a: "}, _EMPTY_, 0),
        ReadHeaderTimeout: time.Second * 5,
    }

    s.mu.Lock()
    s.a2a.server = srv
    s.mu.Unlock()

    // Initialize JetStream resources for task persistence
    s.a2aInitJetStream()

    // Start internal subscriptions for agent registration
    s.a2aInitInternalSubs()

    go func() {
        if err := srv.Serve(hl); err != nil && err != http.ErrServerClosed {
            if !s.isShuttingDown() {
                s.Fatalf("Error starting A2A listener: %v", err)
            }
        }
        srv.Close()
        s.done <- true
    }()
}
```

### 6. JSON-RPC Dispatcher

The core protocol handler — similar to `mqttParse()` (`mqtt.go:742-960`) which dispatches MQTT packet types, but for JSON-RPC methods over HTTP:

```go
// a2aHandleJSONRPC is the main entry point for all A2A JSON-RPC requests.
// POST /a2a → dispatches based on JSON-RPC "method" field.
func (s *Server) a2aHandleJSONRPC(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Parse JSON-RPC request
    body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
    if err != nil {
        s.a2aRespondError(w, nil, ErrParseError, "Failed to read request body")
        return
    }

    var req JSONRPCRequest
    if err := json.Unmarshal(body, &req); err != nil {
        s.a2aRespondError(w, nil, ErrParseError, "Invalid JSON")
        return
    }

    if req.JSONRPC != "2.0" {
        s.a2aRespondError(w, req.ID, ErrInvalidRequest, "Invalid JSON-RPC version")
        return
    }

    // Extract agent name from request params or URL
    agentName := s.a2aExtractAgentName(r, &req)

    // Authenticate the request
    acc, err := s.a2aAuthenticate(r)
    if err != nil {
        s.a2aRespondError(w, req.ID, ErrInternalError, "Authentication failed")
        return
    }

    // Dispatch based on method — analogous to mqttParse switch (mqtt.go:742)
    switch req.Method {

    case "message/send":
        s.a2aHandleMessageSend(w, r, acc, agentName, &req, body)

    case "message/stream":
        s.a2aHandleMessageStream(w, r, acc, agentName, &req, body)

    case "tasks/get":
        s.a2aHandleTasksGet(w, r, acc, &req)

    case "tasks/cancel":
        s.a2aHandleTasksCancel(w, r, acc, agentName, &req)

    default:
        s.a2aRespondError(w, req.ID, ErrMethodNotFound,
            fmt.Sprintf("Unknown method: %s", req.Method))
    }

    // Update request stats (pattern from monitor.go)
    s.mu.Lock()
    s.httpReqStats[A2APath]++
    s.mu.Unlock()
}
```

### 7. HTTP → NATS Translation (The Core Bridge)

The key function that bridges HTTP A2A to internal NATS pub/sub. This is analogous to how MQTT translates `PUBLISH` packets to NATS subjects (`mqttProcessPub`, `mqtt.go`), but in reverse — HTTP requests become NATS request-reply:

```go
// a2aHandleMessageSend translates an HTTP message/send into a NATS request-reply.
func (s *Server) a2aHandleMessageSend(
    w http.ResponseWriter, r *http.Request,
    acc *Account, agentName string,
    req *JSONRPCRequest, body []byte,
) {
    // Build the NATS subject for this agent
    // Pattern: a2a.agents.<agent-name>.message.send
    subject := fmt.Sprintf("a2a.agents.%s.message.send", agentName)

    // Check authorization — does this account have publish permission?
    // Pattern from mqttProcessPub authorization check
    if !acc.canPublish(subject) {
        s.a2aRespondError(w, req.ID, ErrInternalError, "Not authorized")
        return
    }

    // Use internal NATS request-reply
    // The request payload is the full JSON-RPC body
    timeout := s.getOpts().A2A.RequestTimeout
    if timeout == 0 {
        timeout = 30 * time.Second
    }

    // Create an internal client for the request (like mqttJSA pattern)
    resp, err := s.a2a.jsa.request(subject, body, timeout)
    if err != nil {
        if errors.Is(err, ErrTimeout) {
            s.a2aRespondError(w, req.ID, ErrInternalError, "Agent did not respond")
        } else {
            s.a2aRespondError(w, req.ID, ErrInternalError, err.Error())
        }
        return
    }

    // Forward the agent's NATS response as the HTTP response
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    w.Write(resp)
}
```

### 8. Streaming: SSE over NATS Subscriptions

The streaming handler bridges NATS pub/sub to HTTP SSE (Server-Sent Events). This is unique to A2A — MQTT has QoS-based streaming, WebSocket frames the NATS protocol, but A2A needs to translate NATS subject events into SSE:

```go
// a2aHandleMessageStream translates message/stream into NATS subscribe + SSE.
func (s *Server) a2aHandleMessageStream(
    w http.ResponseWriter, r *http.Request,
    acc *Account, agentName string,
    req *JSONRPCRequest, body []byte,
) {
    // Send the initial request to the agent
    subject := fmt.Sprintf("a2a.agents.%s.message.stream", agentName)
    initResp, err := s.a2a.jsa.request(subject, body, 10*time.Second)
    if err != nil {
        s.a2aRespondError(w, req.ID, ErrInternalError, "Agent did not respond")
        return
    }

    // Parse initial response to get the task ID
    var rpcResp JSONRPCResponse
    if err := json.Unmarshal(initResp, &rpcResp); err != nil {
        s.a2aRespondError(w, req.ID, ErrParseError, "Invalid agent response")
        return
    }

    taskData, _ := json.Marshal(rpcResp.Result)
    var task Task
    json.Unmarshal(taskData, &task)

    // Set up SSE response
    flusher, ok := w.(http.Flusher)
    if !ok {
        s.a2aRespondError(w, req.ID, ErrInternalError, "Streaming not supported")
        return
    }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.WriteHeader(http.StatusOK)

    // Send initial task status as first SSE event
    s.a2aWriteSSE(w, flusher, "status-update", initResp)

    // Subscribe to task events on NATS
    // Pattern: a2a.tasks.<task-id>.>
    eventSubject := fmt.Sprintf("a2a.tasks.%s.>", task.ID)

    sub, err := s.a2a.jsa.c.subscribeLocked(eventSubject)
    if err != nil {
        return
    }
    defer sub.unsubscribe()

    // Bridge NATS messages to SSE until task completes or client disconnects
    ctx := r.Context()
    for {
        select {
        case <-ctx.Done():
            // Client disconnected
            return

        case msg := <-sub.msgs:
            // Write NATS message as SSE event
            eventType := "event" // determine from subject suffix
            if strings.HasSuffix(msg.Subject, ".status") {
                eventType = "status-update"
            } else if strings.HasSuffix(msg.Subject, ".artifacts") {
                eventType = "artifact-update"
            }

            s.a2aWriteSSE(w, flusher, eventType, msg.Data)

            // Check if this is the final event
            var statusEvent TaskStatusUpdateEvent
            if err := json.Unmarshal(msg.Data, &statusEvent); err == nil {
                if statusEvent.Final {
                    return
                }
            }
        }
    }
}

func (s *Server) a2aWriteSSE(w http.ResponseWriter, f http.Flusher, event string, data []byte) {
    fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
    f.Flush()
}
```

### 9. Agent Discovery (`.well-known/agent.json`)

The server serves the standard A2A discovery endpoint by querying the internal agent registry:

```go
// a2aHandleWellKnown serves GET /.well-known/agent.json
// Returns agent cards for all agents registered in the requesting account.
func (s *Server) a2aHandleWellKnown(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    acc, err := s.a2aAuthenticate(r)
    if err != nil {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    // Get all agents for this account from registry
    agents := s.a2aGetAgentsForAccount(acc.Name)

    // If a specific agent is requested via query param
    if name := r.URL.Query().Get("agent"); name != "" {
        for _, a := range agents {
            if a.card.Name == name {
                data, _ := json.MarshalIndent(a.card, "", "  ")
                w.Header().Set("Content-Type", "application/json")
                w.Write(data)
                return
            }
        }
        http.NotFound(w, r)
        return
    }

    // Return all agent cards
    cards := make([]AgentCard, 0, len(agents))
    for _, a := range agents {
        cards = append(cards, a.card)
    }
    data, _ := json.MarshalIndent(cards, "", "  ")
    w.Header().Set("Content-Type", "application/json")
    w.Write(data)
}
```

### 10. Internal NATS Subscriptions for Agent Registration

Agents register themselves by publishing to internal NATS subjects. The server watches these to maintain its registry — similar to how MQTT uses `$MQTT.*` internal subjects:

```go
const (
    // Internal subject prefix for A2A (like mqttPrefix = "$MQTT.")
    a2aPrefix = "$A2A."

    // Agents register by publishing their card here
    a2aAgentRegisterSubject   = a2aPrefix + "agent.register"
    a2aAgentUnregisterSubject = a2aPrefix + "agent.unregister"
    a2aAgentHeartbeatSubject  = a2aPrefix + "agent.heartbeat"

    // JetStream stream names for task persistence
    a2aTasksStreamName  = "$A2A_tasks"
    a2aEventsStreamName = "$A2A_events"
)

// a2aInitInternalSubs creates subscriptions on internal subjects
// to track agent registration. Pattern from MQTT session management.
func (s *Server) a2aInitInternalSubs() {
    // Watch for agent registrations
    s.subscribeInternal(a2aAgentRegisterSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, msg []byte) {
        var card AgentCard
        if err := json.Unmarshal(msg, &card); err != nil {
            s.Warnf("A2A: invalid agent card in registration: %v", err)
            return
        }

        s.a2aRegisterAgent(acc, &card)
        s.Noticef("A2A: Agent %q registered in account %q with %d skills",
            card.Name, acc.Name, len(card.Skills))

        if reply != "" {
            s.sendInternalResponse(reply, []byte(`{"registered": true}`))
        }
    })

    // Watch for agent unregistrations
    s.subscribeInternal(a2aAgentUnregisterSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, msg []byte) {
        var unreg struct {
            Name string `json:"name"`
        }
        if err := json.Unmarshal(msg, &unreg); err != nil {
            return
        }
        s.a2aUnregisterAgent(acc, unreg.Name)
        s.Noticef("A2A: Agent %q unregistered from account %q", unreg.Name, acc.Name)
    })

    // Watch for heartbeats
    s.subscribeInternal(a2aAgentHeartbeatSubject, func(sub *subscription, c *client, acc *Account, subject, reply string, msg []byte) {
        var hb struct {
            Name string `json:"name"`
        }
        if err := json.Unmarshal(msg, &hb); err != nil {
            return
        }
        s.a2aUpdateAgentLastSeen(acc, hb.Name)
    })
}
```

### 11. JetStream Task Persistence

Following the MQTT pattern where streams are created per-account (`mqtt.go` stream initialization):

```go
// a2aInitJetStream creates the JetStream streams and KV stores
// needed for A2A task persistence. Pattern from MQTT JetStream init.
func (s *Server) a2aInitJetStream() {
    // These are created lazily per-account when the first agent registers,
    // following the MQTT pattern of per-account session managers.
}

// a2aInitAccountJetStream initializes JetStream resources for a specific account.
func (s *Server) a2aInitAccountJetStream(acc *Account) error {
    // Task state KV store — stores current state of each task
    // Replaces tasks/get with a direct KV lookup
    _, err := acc.addStream(&StreamConfig{
        Name:       a2aTasksStreamName,
        Subjects:   []string{a2aPrefix + "tasks.*.state"},
        Storage:    FileStorage,
        Retention:  LimitsPolicy,
        MaxAge:     s.getOpts().A2A.TaskMaxAge,
        MaxMsgsPer: 1, // Only keep latest state per task
        Replicas:   s.getOpts().A2A.StreamReplicas,
    })
    if err != nil && !errors.Is(err, ErrJetStreamStreamAlreadyUsed) {
        return err
    }

    // Task events stream — full history for streaming replay
    _, err = acc.addStream(&StreamConfig{
        Name:     a2aEventsStreamName,
        Subjects: []string{a2aPrefix + "events.>"},
        Storage:  FileStorage,
        MaxAge:   s.getOpts().A2A.TaskMaxAge,
        Replicas: s.getOpts().A2A.StreamReplicas,
    })
    if err != nil && !errors.Is(err, ErrJetStreamStreamAlreadyUsed) {
        return err
    }

    return nil
}
```

### 12. Monitoring Endpoint (`/a2az`)

Following the pattern at `server.go:3137-3170` and `monitor.go`:

```go
// Path constant — added alongside existing paths in server.go
const A2APath = "/a2az"

// Registration in startMonitoring() — added to the mux alongside /varz, /connz, etc:
// mux.HandleFunc(s.basePath(A2APath), s.HandleA2Az)

// A2Az monitoring data
type A2Az struct {
    Agents     []A2AAgentInfo `json:"agents"`
    TotalTasks uint64         `json:"total_tasks"`
    Port       int            `json:"port"`
}

type A2AAgentInfo struct {
    Name        string    `json:"name"`
    Account     string    `json:"account"`
    Skills      int       `json:"skills"`
    Registered  time.Time `json:"registered"`
    LastSeen    time.Time `json:"last_seen"`
    TaskCount   uint64    `json:"task_count"`
    Healthy     bool      `json:"healthy"`
}

// HandleA2Az returns A2A agent monitoring data.
// Pattern from HandleConnz (monitor.go:718-779).
func (s *Server) HandleA2Az(w http.ResponseWriter, r *http.Request) {
    s.mu.Lock()
    s.httpReqStats[A2APath]++
    s.mu.Unlock()

    accountFilter := r.URL.Query().Get("acc")

    s.a2a.agents.mu.RLock()
    var agents []A2AAgentInfo
    for accName, aam := range s.a2a.agents.byAcct {
        if accountFilter != "" && accName != accountFilter {
            continue
        }
        aam.mu.RLock()
        for _, agent := range aam.agents {
            agents = append(agents, A2AAgentInfo{
                Name:       agent.card.Name,
                Account:    accName,
                Skills:     len(agent.card.Skills),
                Registered: agent.registered,
                LastSeen:   agent.lastSeen,
                TaskCount:  atomic.LoadUint64(&agent.taskCount),
                Healthy:    time.Since(agent.lastSeen) < 2*time.Minute,
            })
        }
        aam.mu.RUnlock()
    }
    s.a2a.agents.mu.RUnlock()

    result := &A2Az{
        Agents:     agents,
        TotalTasks: s.a2aGetTotalTaskCount(),
        Port:       s.getOpts().A2A.Port,
    }

    b, err := json.MarshalIndent(result, "", "  ")
    if err != nil {
        s.Errorf("Error marshaling a2az response: %v", err)
        return
    }
    ResponseHandler(w, r, b)
}
```

### 13. Shutdown Integration

Following the MQTT and WebSocket shutdown pattern in `Server.Shutdown()`:

```go
// In Server.Shutdown() — added alongside MQTT/WebSocket shutdown:
if s.a2a.server != nil {
    s.a2a.server.Close()
}
if s.a2a.listener != nil {
    s.a2a.listener.Close()
}
```

### 14. Config Reload Support

Following the MQTT reload pattern in `server/reload.go`:

```go
// a2aOption implements the option interface for hot reload.
type a2aOption struct {
    noopOption
    newValue A2AOpts
}

func (o *a2aOption) Apply(server *Server) {
    server.mu.Lock()
    server.getOpts().A2A = o.newValue
    server.mu.Unlock()
    server.Noticef("Reloaded A2A configuration")
}
```

### 15. Complete Request Flow Diagram

```
                    HTTP Client
                        │
                        │ POST /a2a
                        │ {"jsonrpc":"2.0","method":"message/send",...}
                        ▼
              ┌─────────────────────┐
              │   s.a2aHandleJSONRPC │  (a2a.go — HTTP handler)
              │                     │
              │  1. Parse JSON-RPC  │
              │  2. Authenticate    │
              │  3. Dispatch method │
              └────────┬────────────┘
                       │
                       ▼
              ┌─────────────────────┐
              │ a2aHandleMessageSend│
              │                     │
              │ Build NATS subject: │
              │ "a2a.agents.X.      │
              │  message.send"      │
              │                     │
              │ nc.Request(subject, │
              │   body, timeout)    │
              └────────┬────────────┘
                       │
          NATS Request-Reply (internal)
                       │
                       ▼
              ┌─────────────────────┐
              │   Agent (NATS sub)  │  (any NATS client)
              │                     │
              │ QueueSubscribe(     │
              │  "a2a.agents.X.     │
              │   message.send",    │
              │  "workers")         │
              │                     │
              │ Process request     │
              │ msg.Respond(result) │
              └────────┬────────────┘
                       │
                  NATS Reply
                       │
                       ▼
              ┌─────────────────────┐
              │ HTTP Response       │
              │                     │
              │ 200 OK              │
              │ {"jsonrpc":"2.0",   │
              │  "result":{...}}    │
              └─────────────────────┘
```

### Comparison with MQTT Integration

| Aspect | MQTT in nats-server | A2A in nats-server |
|---|---|---|
| **Listener** | TCP (binary protocol) | HTTP (JSON-RPC) |
| **Protocol parsing** | `mqttParse()` — binary packet dispatcher | `a2aHandleJSONRPC()` — JSON method dispatcher |
| **Topic → Subject mapping** | `/foo/bar` → `foo.bar`, `+` → `*`, `#` → `>` | Already NATS subjects; HTTP agent name → subject |
| **Session state** | `mqttSession` per client ID | `a2aRegisteredAgent` per agent name |
| **Persistence** | JetStream for QoS 1/2 delivery | JetStream for task state + events |
| **State struct** | `srvMQTT` in `Server` | `srvA2A` in `Server` |
| **Config** | `MQTTOpts` | `A2AOpts` |
| **Internal subjects** | `$MQTT.*` | `$A2A.*` |
| **Monitoring** | (none) | `/a2az` endpoint |
| **Streams** | `$MQTT_msgs`, `$MQTT_sess`, etc. | `$A2A_tasks`, `$A2A_events` |

### What First-Class Gets You vs. Client Library

| Feature | Client Library | First-Class Server |
|---|---|---|
| Agent discovery | Client-side, timeout-based fan-out | Server-managed registry, instant lookup |
| HTTP interop | Requires separate gateway process | Built-in `/.well-known/agent.json` + JSON-RPC |
| Task persistence | Client must set up JetStream streams | Auto-provisioned per account |
| Agent health | No built-in monitoring | `/a2az` endpoint, heartbeat tracking |
| Auth enforcement | NATS subject permissions only | HTTP auth + NATS subject permissions |
| SSE streaming | Not possible (NATS-only) | Server bridges NATS events → SSE |
| Config reload | N/A | Hot reload like MQTT/WebSocket |
| Observability | Manual | Integrated with `/varz`, system events |

---

## A2A-NATS Gateway: Detailed Design

The gateway is the most practical way to bridge HTTP-based A2A clients with NATS-based agents:

```go
// Gateway configuration
type GatewayConfig struct {
    NATSUrl        string
    ListenAddr     string        // e.g., ":8080"
    BasePath       string        // e.g., "/a2a"
    TLS            *tls.Config
    RequestTimeout time.Duration // Default 30s
}

// HTTP endpoints served by the gateway:
//
// GET  /.well-known/agent.json          → Aggregate agent cards from NATS
// POST /a2a                             → JSON-RPC dispatch:
//        method: "message/send"         → nc.Request("a2a.agents.<name>.message.send", ...)
//        method: "message/stream"       → nc.Subscribe("a2a.tasks.<id>.>") + SSE
//        method: "tasks/get"            → nc.Request("a2a.agents.<name>.tasks.get", ...)
//        method: "tasks/cancel"         → nc.Request("a2a.agents.<name>.tasks.cancel", ...)
```

---

## Example: End-to-End Flow

### Scenario: Client asks Recipe Agent to find a recipe

```
1. Discovery:
   Client → NATS Request("a2a.discovery.recipe-agent") → Agent responds with AgentCard

2. Send task:
   Client → NATS Request("a2a.agents.recipe-agent.message.send", {
     jsonrpc: "2.0", method: "message/send",
     params: { message: { role: "user", parts: [{ kind: "text", text: "carbonara" }] } }
   })

3. Agent processes:
   Agent receives on QueueSubscribe("a2a.agents.recipe-agent.message.send", "recipe-workers")
   Agent calls LLM, retrieves recipe
   Agent responds via msg.Respond({
     jsonrpc: "2.0", result: { kind: "task", id: "t1", status: { state: "completed" },
       artifacts: [{ parts: [{ kind: "text", text: "Classic Carbonara: ..." }] }]
     }
   })

4. Client receives response via NATS reply.
```

### Scenario: Streaming with multi-agent collaboration

```
1. Client → Request("a2a.agents.travel-agent.message.stream", { ... "plan a trip to Rome" })
2. Travel Agent creates task T1, responds with Task{id: "T1", status: "submitted"}
3. Client subscribes to "a2a.tasks.T1.status" and "a2a.tasks.T1.artifacts"
4. Travel Agent:
   - Publishes status: "working"
   - Internally calls Recipe Agent: Request("a2a.agents.recipe-agent.message.send",
       { "find Italian restaurants in Rome" })
   - Recipe Agent responds with restaurant list
   - Travel Agent publishes artifact chunk: flight options
   - Travel Agent publishes artifact chunk: hotel options
   - Travel Agent publishes artifact chunk: restaurant recommendations (from Recipe Agent)
   - Publishes status: "completed" (final: true)
5. Client receives all updates in real-time via NATS subscriptions.
```

---

## Summary

| Component | Purpose | Effort |
|---|---|---|
| **`nats-a2a-go` client library** | Go types + helpers for A2A over NATS | Start here |
| **A2A-NATS HTTP Gateway** | Bridge HTTP A2A clients to NATS agents | Medium |
| **JetStream task persistence** | Durable task state and event history | Built into library |
| **NATS account mapping** | Multi-tenant agent isolation | Configuration only |
| **Server-side A2A protocol** | First-class A2A in nats-server | Future |

The recommended path is to start with the **client library** to validate the subject design and agent patterns, then build the **HTTP gateway** for interoperability with the broader A2A ecosystem, and finally consider server-side integration once the patterns are proven.
