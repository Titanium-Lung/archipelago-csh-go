package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"uuid"
)

var ArchipelagoServerPath = "Archipelago-0.6.7/MultiServer.py"
var ShutdownTime = 7200

type ProcessManager struct {
	mutex sync.RWMutex
	rooms map[uuid.UUID]*ArchipelagoServer
}

type ArchipelagoServer struct {
	command  *exec.Cmd
	stdin    io.WriteCloser
	logMutex sync.Mutex
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		rooms: make(map[uuid.UUID]*ArchipelagoServer),
	}
}

func (m *ProcessManager) StartServer(roomId uuid.UUID, archFilePath string, extractFolderPath string, port int) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.rooms[roomId]; exists {
		return fmt.Errorf("room %s already running", roomId)
	}

	cmd := exec.Command("python3", ArchipelagoServerPath, archFilePath, fmt.Sprintf("--port=%d", port), fmt.Sprintf("--auto_shutdown=%d", ShutdownTime))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(), "HOME="+UPLOADS)

	if err := cmd.Start(); err != nil {
		return err
	}

	server := &ArchipelagoServer{command: cmd, stdin: stdin}

	logPath := filepath.Join(extractFolderPath, "server-log.txt")
	// start a thing to write the log
	go writeLog(stdout, logPath, m, roomId, &server.logMutex)
	go writeLog(stderr, logPath, m, roomId, &server.logMutex)

	// time.Sleep(1 * time.Second)
	// if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
	// 	return fmt.Errorf("archipelago server for room %s exited immediately, check %s for details", roomId, logPath)
	// }

	m.rooms[roomId] = server

	fmt.Printf("Starting server on port %d\n", port)

	return nil
}

func (m *ProcessManager) markStopped(roomId uuid.UUID) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if _, exists := m.rooms[roomId]; exists {
		m.rooms[roomId] = nil
	}
}

func writeLog(stdout io.ReadCloser, logPath string, m *ProcessManager, roomId uuid.UUID, logMutex *sync.Mutex) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("failed to open log file: %v", err)
		io.Copy(io.Discard, stdout)
		m.markStopped(roomId)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(stdout)
	// Increase scanner's buffer size
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		nowStr := time.Now().Format("2006-01-02 15:04:05")

		logMutex.Lock()
		fmt.Fprintf(f, "[%s] %s\n", nowStr, line)
		logMutex.Unlock()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error reading stdout for rooms %s: %v", roomId, err)
	}

	m.markStopped(roomId)
}
