# Transferring files over TCP with count data transfer speed

- The server receives the port number on which it will listen for incoming connections from clients.
- The client receives the relative or absolute path to the file to be sent. The filename must be no longer than 4096 bytes in UTF-8 encoding. The file size must not exceed 1 terabyte.
- The client also receives the IP address and port number of the server.
- The client sends the server the filename in UTF-8 encoding, the file size, and its contents. TCP is used for transmission.
- Own protocol using transfer data. First send name file then size file(8 bytes), then 32-KB chunks file while reading file not finished
- The server saves the received file in the "uploads" subdirectory of its current directory. The filename matches the name sent by the client. The server never write outside the "uploads" directory.
- While receiving data from a client, the server  display the instantaneous reception rate and the average rate for the session in the console every 3 seconds. Rates are displayed separately for each active client. If a client has been active for less than 3 seconds, the rate still be displayed for it once. Rate here refers to the number of bytes transferred per unit of time.

## Usage

### Start server

```
go run main.go "port" "network interface"
```

Example 

```
go run main.go "9000" "en0"
```

### Start client

```
go run main.go "ip:port" "file path"
```

Example

```
go run main.go "192.168.0.104:9000" "main.go"
```
