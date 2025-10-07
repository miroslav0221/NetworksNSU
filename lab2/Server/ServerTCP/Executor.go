package ServerTCP

import (
	"errors"
	"fmt"
	"net"
	"os"
)

func getInterface(name string) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	for _, i := range interfaces {
		if i.Name == name {
			addrs, err := i.Addrs()
			if err != nil {
				fmt.Println(err)
				return "", err
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok || ipnet.IP.IsLoopback() {
					continue
				}
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
	}
	return "", err
}

func parseArguments() (string, string, error) {
	if len(os.Args) != 3 {
		fmt.Println("❌ Ошибка в аргументах \n➡️ Ожидается : " +
			"<Название программы> <Порт> <Сетевой интерфейс>")
		return "", "", errors.New("invalid arguments")
	}
	return os.Args[1], os.Args[2], nil
}

func Execute() {

	port, ifaceName, err := parseArguments()
	if err != nil {
		return
	}

	ifaceAddress, err := getInterface(ifaceName)
	if err != nil {
		fmt.Println("Failed select interface", err)
		return
	}
	server := NewServer(ifaceAddress + ":" + port)

	err = server.Start()
	if err != nil {
		fmt.Println(err)
		return
	}
}
