# SOCKS Proxy Server
Proxy Server implementing the SOCKS version 5 standard.
Only one command from the standard is implemented - establish a TCP/IP stream connection without authentication.
The selector is used to manage connections and blocking occurs only on the selector.

## Usage

### Start server

```
go run main.go "port"
```

Example 

```
go run main.go "9000"
```

