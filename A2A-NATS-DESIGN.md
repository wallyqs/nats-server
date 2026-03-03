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

This is the most ambitious approach and should only be pursued after validating the client library approach.

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
