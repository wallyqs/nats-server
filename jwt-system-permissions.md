# JWT System Permissions for NATS Server Operations

A reference for the subjects useful when configuring JWT permissions for system-level operations.

## JetStream API (`$JS.API.>`)

### Stream Management

| Subject | Description |
|---|---|
| `$JS.API.INFO` | Account JetStream information |
| `$JS.API.STREAM.CREATE.*` | Create streams |
| `$JS.API.STREAM.UPDATE.*` | Update streams |
| `$JS.API.STREAM.DELETE.*` | Delete streams |
| `$JS.API.STREAM.PURGE.*` | Purge streams |
| `$JS.API.STREAM.NAMES` | List stream names |
| `$JS.API.STREAM.LIST` | List streams with details |
| `$JS.API.STREAM.INFO.*` | Get stream information |
| `$JS.API.STREAM.SNAPSHOT.*` | Create stream snapshots |
| `$JS.API.STREAM.RESTORE.*` | Restore streams from snapshots |
| `$JS.API.STREAM.MSG.GET.*` | Get individual messages |
| `$JS.API.STREAM.MSG.DELETE.*` | Delete messages |
| `$JS.API.STREAM.PEER.REMOVE.*` | Remove stream peers (cluster) |
| `$JS.API.STREAM.LEADER.STEPDOWN.*` | Stream leader stepdown |

### Consumer Management

| Subject | Description |
|---|---|
| `$JS.API.CONSUMER.CREATE.*` | Create consumers |
| `$JS.API.CONSUMER.DURABLE.CREATE.*.*` | Create durable consumers |
| `$JS.API.CONSUMER.DELETE.*.*` | Delete consumers |
| `$JS.API.CONSUMER.INFO.*.*` | Get consumer information |
| `$JS.API.CONSUMER.NAMES.*` | List consumer names |
| `$JS.API.CONSUMER.LIST.*` | List consumers with details |
| `$JS.API.CONSUMER.PAUSE.*.*` | Pause/unpause consumers |
| `$JS.API.CONSUMER.MSG.NEXT.*.*` | Request next message |
| `$JS.API.CONSUMER.UNPIN.*.*` | Unpin consumers |
| `$JS.API.CONSUMER.LEADER.STEPDOWN.*.*` | Consumer leader stepdown |

### Direct Get

| Subject | Description |
|---|---|
| `$JS.API.DIRECT.GET.*` | Direct message retrieval |
| `$JS.API.DIRECT.GET.*.>` | Direct get last by subject |

### System-Only JetStream Operations

These require the caller to be connected to the system account.

| Subject | Description |
|---|---|
| `$JS.API.META.LEADER.STEPDOWN` | JetStream meta leader stepdown |
| `$JS.API.SERVER.REMOVE` | Remove server from cluster |
| `$JS.API.ACCOUNT.PURGE.*` | Purge account's JetStream content |
| `$JS.API.ACCOUNT.STREAM.MOVE.*.*` | Move streams off server |
| `$JS.API.ACCOUNT.STREAM.CANCEL_MOVE.*.*` | Cancel stream move |

## Server Monitoring (`$SYS.REQ.SERVER.>`)

### Fan-Out Requests (ping all servers)

| Subject | Description |
|---|---|
| `$SYS.REQ.SERVER.PING.STATSZ` | Server statistics |
| `$SYS.REQ.SERVER.PING.VARZ` | Server variables |
| `$SYS.REQ.SERVER.PING.CONNZ` | Connection information |
| `$SYS.REQ.SERVER.PING.ROUTEZ` | Route information |
| `$SYS.REQ.SERVER.PING.GATEWAYZ` | Gateway information |
| `$SYS.REQ.SERVER.PING.LEAFZ` | Leaf node information |
| `$SYS.REQ.SERVER.PING.SUBSZ` | Subscription information |
| `$SYS.REQ.SERVER.PING.ACCOUNTZ` | Account information |
| `$SYS.REQ.SERVER.PING.PROFILEZ` | Profiling data |

### Direct Server Requests

| Subject | Description |
|---|---|
| `$SYS.REQ.SERVER.<server_id>.STATSZ` | Statistics for specific server |
| `$SYS.REQ.SERVER.<server_id>.RELOAD` | Reload specific server configuration |

## Account Management (`$SYS.REQ.ACCOUNT.>`)

| Subject | Description |
|---|---|
| `$SYS.REQ.ACCOUNT.<account_id>.CONNZ` | Account connections |
| `$SYS.REQ.ACCOUNT.<account_id>.STATZ` | Account statistics |
| `$SYS.REQ.ACCOUNT.PING.CONNZ` | Fan-out account connections |
| `$SYS.REQ.ACCOUNT.PING.STATZ` | Fan-out account statistics |

## JWT/Claims Management (`$SYS.REQ.CLAIMS.>`)

| Subject | Description |
|---|---|
| `$SYS.REQ.CLAIMS.UPDATE` | Push updated account JWTs |
| `$SYS.REQ.CLAIMS.PACK` | Pack claims |
| `$SYS.REQ.CLAIMS.LIST` | List claims |
| `$SYS.REQ.CLAIMS.DELETE` | Delete claims |
| `$SYS.REQ.ACCOUNT.<account_id>.CLAIMS.LOOKUP` | Lookup specific account claims |
| `$SYS.REQ.ACCOUNT.<account_id>.CLAIMS.UPDATE` | Update specific account claims |

## User Info (`$SYS.REQ.USER.>`)

| Subject | Description |
|---|---|
| `$SYS.REQ.USER.INFO` | Generic user information |
| `$SYS.REQ.USER.<user_id>.INFO` | Specific user information |

## Debug (`$SYS.DEBUG.SUBSCRIBERS`)

| Subject | Description |
|---|---|
| `$SYS.DEBUG.SUBSCRIBERS` | Count subscribers for a subject across the cluster |

## System Events (subscribe-side)

### Server Events

| Subject | Description |
|---|---|
| `$SYS.SERVER.<server_id>.STATSZ` | Server statistics events |
| `$SYS.SERVER.<server_id>.SHUTDOWN` | Server shutdown notification |
| `$SYS.SERVER.<server_id>.LAMEDUCK` | Lame duck mode notification |
| `$SYS.SERVER.<server_id>.CLIENT.AUTH.ERR` | Authentication errors |

### Account Events

| Subject | Description |
|---|---|
| `$SYS.ACCOUNT.<account_id>.CONNECT` | Client connection |
| `$SYS.ACCOUNT.<account_id>.DISCONNECT` | Client disconnection |
| `$SYS.ACCOUNT.<account_id>.LEAFNODE.CONNECT` | Leaf node connection |
| `$SYS.ACCOUNT.<account_id>.SERVER.CONNS` | Account server connections |
| `$SYS.ACCOUNT.<account_id>.CLAIMS.UPDATE` | Account claims updated |

### JetStream Events

| Subject | Description |
|---|---|
| `$JS.EVENT.ADVISORY.>` | Advisory events |
| `$JS.EVENT.METRIC.>` | Metric events |

## Recommended Permission Set

### Publish (request-side)

```
$JS.API.>                  # All JetStream operations
$SYS.REQ.SERVER.>          # All server monitoring requests
$SYS.REQ.ACCOUNT.>         # All account management requests
$SYS.REQ.CLAIMS.>          # JWT/claims management
$SYS.REQ.USER.>            # User info requests
$SYS.DEBUG.SUBSCRIBERS     # Debug subscriber counts
```

### Subscribe (response + events)

```
$SYS.SERVER.>              # Server events
$SYS.ACCOUNT.>             # Account events
$JS.EVENT.>                # JetStream advisories and metrics
_INBOX.>                   # Reply inboxes for request/reply
```
