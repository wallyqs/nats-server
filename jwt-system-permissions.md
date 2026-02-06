# JWT System Permissions for NATS Server Operations

A reference for the subjects useful when configuring JWT permissions for system-level operations.

## JetStream API (`$JS.API.>`)

### Stream Management

| Subject | Description | Access Required |
|---|---|---|
| `$JS.API.INFO` | Account JetStream information | Publish |
| `$JS.API.STREAM.CREATE.*` | Create streams | Publish |
| `$JS.API.STREAM.UPDATE.*` | Update streams | Publish |
| `$JS.API.STREAM.DELETE.*` | Delete streams | Publish |
| `$JS.API.STREAM.PURGE.*` | Purge streams | Publish |
| `$JS.API.STREAM.NAMES` | List stream names | Publish |
| `$JS.API.STREAM.LIST` | List streams with details | Publish |
| `$JS.API.STREAM.INFO.*` | Get stream information | Publish |
| `$JS.API.STREAM.SNAPSHOT.*` | Create stream snapshots | Publish |
| `$JS.API.STREAM.RESTORE.*` | Restore streams from snapshots | Publish |
| `$JS.API.STREAM.MSG.GET.*` | Get individual messages | Publish |
| `$JS.API.STREAM.MSG.DELETE.*` | Delete messages | Publish |
| `$JS.API.STREAM.PEER.REMOVE.*` | Remove stream peers (cluster) | Publish |
| `$JS.API.STREAM.LEADER.STEPDOWN.*` | Stream leader stepdown | Publish |

### Consumer Management

| Subject | Description | Access Required |
|---|---|---|
| `$JS.API.CONSUMER.CREATE.*` | Create consumers | Publish |
| `$JS.API.CONSUMER.DURABLE.CREATE.*.*` | Create durable consumers | Publish |
| `$JS.API.CONSUMER.DELETE.*.*` | Delete consumers | Publish |
| `$JS.API.CONSUMER.INFO.*.*` | Get consumer information | Publish |
| `$JS.API.CONSUMER.NAMES.*` | List consumer names | Publish |
| `$JS.API.CONSUMER.LIST.*` | List consumers with details | Publish |
| `$JS.API.CONSUMER.PAUSE.*.*` | Pause/unpause consumers | Publish |
| `$JS.API.CONSUMER.MSG.NEXT.*.*` | Request next message | Publish |
| `$JS.API.CONSUMER.UNPIN.*.*` | Unpin consumers | Publish |
| `$JS.API.CONSUMER.LEADER.STEPDOWN.*.*` | Consumer leader stepdown | Publish |

### Direct Get

| Subject | Description | Access Required |
|---|---|---|
| `$JS.API.DIRECT.GET.*` | Direct message retrieval | Publish |
| `$JS.API.DIRECT.GET.*.>` | Direct get last by subject | Publish |

### System-Only JetStream Operations

These require the caller to be connected to the system account.

| Subject | Description | Access Required |
|---|---|---|
| `$JS.API.META.LEADER.STEPDOWN` | JetStream meta leader stepdown | Publish (system account only) |
| `$JS.API.SERVER.REMOVE` | Remove server from cluster | Publish (system account only) |
| `$JS.API.ACCOUNT.PURGE.*` | Purge account's JetStream content | Publish (system account only) |
| `$JS.API.ACCOUNT.STREAM.MOVE.*.*` | Move streams off server | Publish (system account only) |
| `$JS.API.ACCOUNT.STREAM.CANCEL_MOVE.*.*` | Cancel stream move | Publish (system account only) |

## Server Monitoring (`$SYS.REQ.SERVER.>`)

### Fan-Out Requests (ping all servers)

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.REQ.SERVER.PING.STATSZ` | Server statistics | Publish |
| `$SYS.REQ.SERVER.PING.VARZ` | Server variables | Publish |
| `$SYS.REQ.SERVER.PING.CONNZ` | Connection information | Publish |
| `$SYS.REQ.SERVER.PING.ROUTEZ` | Route information | Publish |
| `$SYS.REQ.SERVER.PING.GATEWAYZ` | Gateway information | Publish |
| `$SYS.REQ.SERVER.PING.LEAFZ` | Leaf node information | Publish |
| `$SYS.REQ.SERVER.PING.SUBSZ` | Subscription information | Publish |
| `$SYS.REQ.SERVER.PING.ACCOUNTZ` | Account information | Publish |
| `$SYS.REQ.SERVER.PING.PROFILEZ` | Profiling data | Publish |

### Direct Server Requests

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.REQ.SERVER.<server_id>.STATSZ` | Statistics for specific server | Publish |
| `$SYS.REQ.SERVER.<server_id>.RELOAD` | Reload specific server configuration | Publish |

## Account Management (`$SYS.REQ.ACCOUNT.>`)

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.REQ.ACCOUNT.<account_id>.CONNZ` | Account connections | Publish |
| `$SYS.REQ.ACCOUNT.<account_id>.STATZ` | Account statistics | Publish |
| `$SYS.REQ.ACCOUNT.PING.CONNZ` | Fan-out account connections | Publish |
| `$SYS.REQ.ACCOUNT.PING.STATZ` | Fan-out account statistics | Publish |

## JWT/Claims Management (`$SYS.REQ.CLAIMS.>`)

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.REQ.CLAIMS.UPDATE` | Push updated account JWTs | Publish |
| `$SYS.REQ.CLAIMS.PACK` | Pack claims | Publish |
| `$SYS.REQ.CLAIMS.LIST` | List claims | Publish |
| `$SYS.REQ.CLAIMS.DELETE` | Delete claims | Publish |
| `$SYS.REQ.ACCOUNT.<account_id>.CLAIMS.LOOKUP` | Lookup specific account claims | Publish |
| `$SYS.REQ.ACCOUNT.<account_id>.CLAIMS.UPDATE` | Update specific account claims | Publish |

## User Info (`$SYS.REQ.USER.>`)

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.REQ.USER.INFO` | Generic user information | Publish |
| `$SYS.REQ.USER.<user_id>.INFO` | Specific user information | Publish |

## Debug (`$SYS.DEBUG.SUBSCRIBERS`)

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.DEBUG.SUBSCRIBERS` | Count subscribers for a subject across the cluster | Publish |

## System Events (subscribe-side)

### Server Events

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.SERVER.<server_id>.STATSZ` | Server statistics events | Subscribe |
| `$SYS.SERVER.<server_id>.SHUTDOWN` | Server shutdown notification | Subscribe |
| `$SYS.SERVER.<server_id>.LAMEDUCK` | Lame duck mode notification | Subscribe |
| `$SYS.SERVER.<server_id>.CLIENT.AUTH.ERR` | Authentication errors | Subscribe |

### Account Events

| Subject | Description | Access Required |
|---|---|---|
| `$SYS.ACCOUNT.<account_id>.CONNECT` | Client connection | Subscribe |
| `$SYS.ACCOUNT.<account_id>.DISCONNECT` | Client disconnection | Subscribe |
| `$SYS.ACCOUNT.<account_id>.LEAFNODE.CONNECT` | Leaf node connection | Subscribe |
| `$SYS.ACCOUNT.<account_id>.SERVER.CONNS` | Account server connections | Subscribe |
| `$SYS.ACCOUNT.<account_id>.CLAIMS.UPDATE` | Account claims updated | Subscribe |

### JetStream Events

| Subject | Description | Access Required |
|---|---|---|
| `$JS.EVENT.ADVISORY.>` | Advisory events | Subscribe |
| `$JS.EVENT.METRIC.>` | Metric events | Subscribe |

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
