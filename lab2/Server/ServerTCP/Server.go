package ServerTCP

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	sizeChunk    = 32 * 1024
	sizeint64    = 8
	sizeNameFile = 4096
	startTime    = 0
	period       = 3
	KB           = 1024
	MB           = 1024 * 1024
	GB           = 1024 * 1024 * 1024
	successful   = 200
	failed       = 400
)

type FileInfo struct {
	sizeFile int64
	filename string
	fd       *os.File
}

type Server struct {
	listenAddr  string
	listener    net.Listener
	quit        chan struct{}
	currentTime int
}

func NewServer(listenAddr string) *Server {
	return &Server{
		listenAddr:  listenAddr,
		quit:        make(chan struct{}),
		currentTime: startTime,
	}
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	s.listener = listener

	go s.accept()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		fmt.Println("\n🛑 Получен сигнал ОС, сервер завершается...")
	case <-s.quit:
		fmt.Println("\n🛑 Получен внутренний сигнал, сервер завершается...")
	}

	return nil
}

func (s *Server) accept() {
	for {
		fmt.Println("\n────────────────────────────────────────────")
		fmt.Println("🌐 Сервер слушает на:", s.listener.Addr().String())
		fmt.Println("⏳ Ожидание входящих подключений...")
		fmt.Println("────────────────────────────────────────────")

		connection, err := s.listener.Accept()
		if err != nil {
			fmt.Println("❌ Ошибка при подключении:", err.Error())
			continue
		}

		fmt.Printf("\n✅ Подключение принято от: %s\n", connection.RemoteAddr().String())
		fmt.Println("────────────────────────────────────────────")
		go s.handleConnection(connection)
	}
}

func (s *Server) sendSuccessful(conn net.Conn) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, uint32(successful))
	_, err := conn.Write(buf)
	if err != nil {
		fmt.Println("❌ Ошибка отправки сообщения : ", err)
		return
	}
	fmt.Println("✅ Сообщение успешно отправлено")
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	fmt.Println("📥 Начинаем обработку нового файла...")
	fmt.Println("────────────────────────────────────────────")

	fileInfo, err := s.receiveFileInfo(conn)
	if err != nil {
		fmt.Println("❌ Ошибка получения информации о файле:", err)
		return
	}
	defer fileInfo.fd.Close()

	err = s.receiveFileData(conn, fileInfo)
	if err != nil {
		fmt.Println("❌ Ошибка приёма данных файла:", err)
		return
	}

	s.sendSuccessful(conn)

	fmt.Println("────────────────────────────────────────────")
	fmt.Printf("🎉 Файл \"%s\" успешно получен \n", fileInfo.filename)
	fmt.Println("────────────────────────────────────────────\n")
}

func (s *Server) printSize(fileSize int64) {
	if fileSize > GB {
		fmt.Printf("📦 Размер файла: %.2f ГБ\n", float64(fileSize)/float64(GB))
	} else if fileSize > MB {
		fmt.Printf("📦 Размер файла: %.2f МБ\n", float64(fileSize)/float64(MB))
	} else if fileSize > KB {
		fmt.Printf("📦 Размер файла: %.2f КБ\n", float64(fileSize)/float64(KB))
	} else {
		fmt.Printf("📦 Размер файла: %d Б\n", fileSize)
	}
}

func (s *Server) receiveFileInfo(conn net.Conn) (*FileInfo, error) {
	nameBuf := make([]byte, sizeNameFile)
	_, err := io.ReadFull(conn, nameBuf)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать имя файла: %v", err)
	}

	filename := string(bytes.TrimRight(nameBuf, "\x00"))

	fmt.Printf("📄 Имя файла: %s\n", filename)

	sizeBuf := make([]byte, sizeint64)
	_, err = io.ReadFull(conn, sizeBuf)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать размер файла: %v", err)
	}

	fileSize := int64(binary.LittleEndian.Uint64(sizeBuf))

	s.printSize(fileSize)

	path := s.getPath(filename)
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать файл: %v", err)
	}

	return NewFileInfo(filename, file, fileSize), nil
}

func printSpeed(speed float64) {
	if speed > GB {
		fmt.Printf("⚡ %.2f GB/s\n", speed/float64(GB))
	} else if speed > MB {
		fmt.Printf("⚡ %.2f MB/s\n", speed/float64(MB))
	} else if speed > KB {
		fmt.Printf("⚡ %.2f KB/s\n", speed/float64(KB))
	} else {
		fmt.Printf("⚡ %.2f B/s\n", speed)
	}
}

func (s *Server) receiveFileData(conn net.Conn, fileInfo *FileInfo) error {
	var receivedBytes int64

	go s.updatingTime()
	lastCheck := s.currentTime
	receivedBytesLastCheck := int64(0)

	for receivedBytes < fileInfo.sizeFile {
		buf := make([]byte, sizeChunk)
		n, err := conn.Read(buf) //
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("ошибка чтения чанка: %v", err)
		}

		_, err = fileInfo.fd.Write(buf[:n])
		if err != nil {
			return fmt.Errorf("ошибка записи чанка: %v", err)
		}

		receivedBytes += int64(n)

		if s.currentTime-lastCheck >= period {
			fmt.Println("\n────────────── Статистика ──────────────")

			fmt.Println("📄Имя файла: ", fileInfo.filename)
			avgSpeed := float64(receivedBytes) / float64(s.currentTime)
			fmt.Print("📊 Средняя скорость: ")
			printSpeed(avgSpeed)

			momentSpeed := float64(receivedBytes-receivedBytesLastCheck) / float64(period)
			fmt.Print("🚀 Мгновенная скорость: ")
			printSpeed(momentSpeed)

			lastCheck = s.currentTime
			receivedBytesLastCheck = receivedBytes

			fmt.Printf("📥 Прогресс: %d/%d байт (%.2f%%)\n",
				receivedBytes, fileInfo.sizeFile,
				float64(receivedBytes)/float64(fileInfo.sizeFile)*100)

			fmt.Println("────────────────────────────────────────")
		}
	}

	if receivedBytes != fileInfo.sizeFile {
		return fmt.Errorf("несовпадение размера файла: ожидалось %d, получено %d",
			fileInfo.sizeFile, receivedBytes)
	}

	fmt.Println("\n────────────── Итог ──────────────")
	fmt.Println("📄Имя файла: ", fileInfo.filename)
	avgSpeed := float64(receivedBytes) / float64(1)
	if s.currentTime != 0 {
		avgSpeed = float64(receivedBytes) / float64(s.currentTime)
	}
	fmt.Print("🏁 Итоговая средняя скорость: ")
	printSpeed(avgSpeed)
	fmt.Println("──────────────────────────────────")

	return nil
}

func (s *Server) getPath(namefile string) string {
	relativePath := "uploads/"
	absolutePath, _ := filepath.Abs(relativePath)
	os.MkdirAll(absolutePath, 0755)
	path := filepath.Join(absolutePath, namefile)
	fmt.Println("💾 Сохраняем файл в:", path)
	return path
}

func NewFileInfo(filename string, fd *os.File, size int64) *FileInfo {
	return &FileInfo{
		filename: filename,
		fd:       fd,
		sizeFile: size,
	}
}

func (s *Server) updatingTime() {
	start := time.Now()
	for {
		time.Sleep(time.Second)
		s.currentTime = int(time.Since(start).Seconds())
	}
}
