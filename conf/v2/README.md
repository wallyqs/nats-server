# conf/v2

Package `v2` is a configuration parser for the NATS server that builds a full AST with position information and comment preservation, provides `encoding/json`-style `Marshal`/`Unmarshal` support, and maintains backwards compatibility with the v1 `conf` package.

```go
import conf "github.com/nats-io/nats-server/v2/conf/v2"
```

## Backwards-Compatible Parse API

Drop-in replacements for the v1 `conf.Parse` / `conf.ParseFile` functions. Values are returned as `map[string]any` with raw Go types (`string`, `bool`, `int64`, `float64`, `time.Time`, `[]any`, `map[string]any`).

```go
m, err := conf.Parse(`
  port: 4222
  max_payload: 1MB
`)
fmt.Println(m["port"])        // 4222 (int64)
fmt.Println(m["max_payload"]) // 1048576 (int64)
```

```go
m, err := conf.ParseFile("/etc/nats/nats-server.conf")
```

Pedantic variants wrap values in tokens that carry source position info:

```go
m, err := conf.ParseWithChecks(data)
m, err := conf.ParseFileWithChecks(path)
m, digest, err := conf.ParseFileWithChecksDigest(path) // + SHA-256 digest
```

## Unmarshal

Decode NATS config directly into Go structs. The target must be a non-nil pointer to a struct.

```go
type Config struct {
    Host        string        `conf:"host"`
    Port        int           `conf:"port"`
    MaxPayload  int64         `conf:"max_payload"`
    Debug       bool          `conf:"debug,omitempty"`
    WriteDeadline time.Duration `conf:"write_deadline"`
    Auth        *AuthConfig   `conf:"authorization"`
}

type AuthConfig struct {
    User string `conf:"user"`
    Pass string `conf:"password"`
}

var cfg Config
err := conf.Unmarshal([]byte(`
  host: 0.0.0.0
  port: 4222
  max_payload: 1MB
  write_deadline: "2s"
  authorization {
    user: admin
    password: s3cret
  }
`), &cfg)
```

### Unmarshal from file

```go
err := conf.UnmarshalFile("/etc/nats/nats-server.conf", &cfg)
```

### Strict mode

Unknown config keys produce an error instead of being silently ignored:

```go
err := conf.UnmarshalWith(data, &cfg, &conf.UnmarshalOptions{Strict: true})
err := conf.UnmarshalFileWith(path, &cfg, &conf.UnmarshalOptions{Strict: true})
```

## Marshal

Encode Go structs into NATS config text. The input must be a struct or non-nil pointer to a struct.

```go
type Config struct {
    Host  string `conf:"host"`
    Port  int    `conf:"port"`
    Debug bool   `conf:"debug,omitempty"`
}

data, err := conf.Marshal(Config{Host: "0.0.0.0", Port: 4222})
// Output:
// host: 0.0.0.0
// port: 4222
```

### MarshalIndent

```go
data, err := conf.MarshalIndent(cfg, "", "  ")
```

## AST Round-Trip

Parse config into an AST, inspect or modify it, then emit it back to text. Use `ParseASTRaw` / `ParseASTRawFile` to preserve variable references, include directives, comments, and original formatting for round-trip fidelity.

```go
doc, err := conf.ParseASTRaw(`
# Server settings
port = 4222
max_payload = 1MB  # max message size
`)

// Walk the AST
for _, item := range doc.Items {
    switch n := item.(type) {
    case *conf.KeyValueNode:
        fmt.Printf("key=%s\n", n.Key.Name)
    case *conf.CommentNode:
        fmt.Printf("comment: %s\n", n.Text)
    }
}

// Emit back to text (preserves comments, formatting, separators)
out, err := conf.Emit(doc)
// or equivalently: out, err := doc.Emit()
```

### Parse AST variants

| Function | Description |
|---|---|
| `ParseAST(data)` | Parse string, resolve variables and includes |
| `ParseASTFile(path)` | Parse file, resolve variables and includes |
| `ParseASTRaw(data)` | Parse string, preserve variables and includes as AST nodes |
| `ParseASTRawFile(path)` | Parse file, preserve variables and includes as AST nodes |

## Struct Tags

Fields are matched to config keys using the `conf` struct tag. Untagged exported fields are matched by their lowercased name. Key matching is case-insensitive.

| Tag | Meaning |
|---|---|
| `conf:"name"` | Use `name` as the config key |
| `conf:"name,omitempty"` | Skip field when zero-valued (Marshal) |
| `conf:"-"` | Skip field entirely |
| `conf:",omitempty"` | Use lowercased field name, omit when empty |

## Custom Marshaler / Unmarshaler

Implement the `Marshaler` or `Unmarshaler` interface for custom serialization logic:

```go
// Marshaler produces raw NATS config text for a field value.
type Marshaler interface {
    MarshalConfig() ([]byte, error)
}

// Unmarshaler receives the raw parsed value for custom decoding.
type Unmarshaler interface {
    UnmarshalConfig(v any) error
}
```

Example:

```go
type LogLevel int

func (l *LogLevel) UnmarshalConfig(v any) error {
    s, ok := v.(string)
    if !ok {
        return fmt.Errorf("expected string for log level")
    }
    switch strings.ToLower(s) {
    case "debug": *l = 0
    case "info":  *l = 1
    case "error": *l = 2
    default:
        return fmt.Errorf("unknown log level: %s", s)
    }
    return nil
}

func (l LogLevel) MarshalConfig() ([]byte, error) {
    names := []string{"debug", "info", "error"}
    return []byte(names[l]), nil
}
```

## Supported Types

The Unmarshal/Marshal engines support:

- `string`, `bool`
- All integer types (`int`, `int8`–`int64`, `uint`, `uint8`–`uint64`) with overflow checking
- `float32`, `float64`
- `time.Duration` — strings via `time.ParseDuration`; integers treated as seconds on unmarshal
- `time.Time` — ISO 8601 Zulu format (`2006-01-02T15:04:05Z`)
- Slices, maps (`map[string]T`), nested structs, pointer variants
- Embedded (anonymous) structs — fields are promoted
- `any` / `interface{}`
- Types implementing `Marshaler` / `Unmarshaler`

Integer values in config support size suffixes: `k`, `kb`/`ki`/`kib`, `m`, `mb`/`mi`/`mib`, `g`, `gb`/`gi`/`gib`, `t`, `tb`/`ti`/`tib`, `p`, `pb`/`pi`/`pib`, `e`, `eb`/`ei`/`eib`.

## AST Node Types

| Node | Description |
|---|---|
| `Document` | Root node; contains top-level items |
| `KeyValueNode` | Key-value pair with optional trailing comment |
| `KeyNode` | Key name with separator style (`=`, `:`, space) |
| `MapNode` | `{ }` block of key-value pairs |
| `ArrayNode` | `[ ]` list of values |
| `StringNode` | String value |
| `IntegerNode` | Integer value (with optional size suffix) |
| `FloatNode` | Float value |
| `BoolNode` | Boolean (`true`/`false`, `yes`/`no`, `on`/`off`) |
| `DatetimeNode` | ISO 8601 Zulu datetime |
| `CommentNode` | Comment (`#` or `//` style) |
| `VariableNode` | Variable reference (`$name`), raw mode only |
| `IncludeNode` | Include directive, raw mode only |
| `BlockStringNode` | Multi-line block string `(...)` |

## Public API Reference

### Backwards-Compatible Parse

- `Parse(data string) (map[string]any, error)`
- `ParseWithChecks(data string) (map[string]any, error)`
- `ParseFile(fp string) (map[string]any, error)`
- `ParseFileWithChecks(fp string) (map[string]any, error)`
- `ParseFileWithChecksDigest(fp string) (map[string]any, string, error)`

### AST Parse

- `ParseAST(data string) (*Document, error)`
- `ParseASTFile(fp string) (*Document, error)`
- `ParseASTRaw(data string) (*Document, error)`
- `ParseASTRawFile(fp string) (*Document, error)`

### AST Emit

- `Emit(doc *Document) ([]byte, error)`
- `(*Document).Emit() ([]byte, error)`

### Marshal

- `Marshal(v any) ([]byte, error)`
- `MarshalIndent(v any, prefix, indent string) ([]byte, error)`

### Unmarshal

- `Unmarshal(data []byte, v any) error`
- `UnmarshalWith(data []byte, v any, opts *UnmarshalOptions) error`
- `UnmarshalFile(path string, v any) error`
- `UnmarshalFileWith(path string, v any, opts *UnmarshalOptions) error`

### Interfaces

- `Marshaler` — `MarshalConfig() ([]byte, error)`
- `Unmarshaler` — `UnmarshalConfig(v any) error`

### Types

- `UnmarshalOptions` — `Strict bool`
- `Position` — `Line int`, `Column int`, `File string`
- `Node` interface — `Position() Position`, `Type() string`
- `KeySeparator` — `SepEquals`, `SepColon`, `SepSpace`
- `CommentStyle` — `CommentHash`, `CommentSlash`
