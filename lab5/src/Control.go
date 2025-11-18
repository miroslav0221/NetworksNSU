package src


func ExecuteServer() {
	server := NewServer()
	port := parseArgs()
	if port == -1 {
		return
	}
	server.InitSocket(port)
	server.InitSelecter()
	server.WaitEvents();
}