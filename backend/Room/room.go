package Room

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	helpers "github.com/Titanium-lung/archipelago-csh-go-backend/Helpers"
	pm "github.com/Titanium-lung/archipelago-csh-go-backend/ProcessManager"
	cshAuth "github.com/computersciencehouse/csh-auth/v2"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RestartRoom(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager, ServerPort int, PortRange int) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	var archFilePath, extractFolderPath string
	var oldport int
	err = db.QueryRow(c.Request.Context(), "SELECT arch_file_path, extract_folder_path, port FROM rooms WHERE room_id = $1", roomId).Scan(&archFilePath, &extractFolderPath, &oldport)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get info from database: %s", err.Error())})
		return
	}

	if _, err := os.Stat(archFilePath); errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("No archipelago file at path: %s", err.Error())})
		return
	}

	if !processManager.IsRunning(roomUUID) {
		if restarting, _ := processManager.IsRestarting(roomUUID); restarting {
			c.JSON(http.StatusNoContent, gin.H{"error": "Server already restarting"})
			return
		}
		processManager.SetRestarting(roomUUID, true)

		var port int
		// Generate ports and find an available one
		ports := rand.Perm(ServerPort + PortRange - 1 - ServerPort)
		ports = append([]int{oldport - ServerPort}, ports...)
		for _, tryport := range ports {
			if helpers.CheckPort(tryport) {
				port = tryport
				break
			}
		}

		if port == 0 {
			processManager.SetRestarting(roomUUID, false)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not find an available port in range, try again later"})
			return
		}

		port += ServerPort

		if port != oldport {
			_, err = db.Exec(c.Request.Context(), "UPDATE rooms SET port = $1 WHERE room_id = $2", port, roomId)
			if err != nil {
				processManager.SetRestarting(roomUUID, false)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update port in database: " + err.Error()})
				return
			}
		}

		if err = processManager.StartServer(roomUUID, archFilePath, extractFolderPath, port); err != nil {
			processManager.SetRestarting(roomUUID, false)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to restart archipelago server: %s", err.Error())})
			return
		}

		processManager.SetRestarting(roomUUID, false)

		c.JSON(http.StatusOK, gin.H{"message": "Server restarted", "port": port})
	} else {
		c.JSON(http.StatusNoContent, gin.H{"error": "Server already running"})
		return
	}
}

func GetLog(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var extractFolderPath string
	err = db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	logFile := filepath.Join(extractFolderPath, "server-log.txt")
	file, err := os.Open(logFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to open log file: %s", err.Error())})
		return
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read log file: %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"lines": lines})
}

func ServerCommand(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	if !processManager.IsRunning(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Archipelago server not running"})
		return
	}

	var admin string
	err = db.QueryRow(c.Request.Context(), "SELECT admin FROM rooms WHERE room_id = $1", roomId).Scan(&admin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	userValue, exists := c.Get("cshauth")
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Couldn't get logged in user"})
		return
	}

	user, ok := userValue.(*cshAuth.Claims)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Couldn't get logged in user"})
		return
	}

	if admin != user.Uuid {
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the admin of this room"})
		return
	}

	var data map[string]any

	if err = c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get command from JSON: %s", err.Error())})
		return
	}

	command := data["command"].(string)
	err = processManager.SendCommand(roomUUID, command)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to send command to server: %s", err.Error())})
		return
	}

	if args := strings.Split(command, " "); len(args) >= 2 && strings.HasPrefix(command, "/release") {
		gameName := strings.TrimSpace(strings.ToLower(command[strings.Index(command, " "):]))
		db.Exec(c.Request.Context(), "INSERT INTO released_games VALUES ($1, $2)", gameName, roomId)
	}
}

func StreamLog(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var extractFolderPath string
	err = db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	logFile := filepath.Join(extractFolderPath, "server-log.txt")
	file, err := os.Open(logFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to open log file: %s", err.Error())})
		return
	}
	defer file.Close()

	if _, err = file.Seek(0, io.SeekEnd); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read log file: %s", err.Error())})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	reader := bufio.NewReader(file)
	var partial strings.Builder

	c.Stream(func(w io.Writer) bool {
		line, err := reader.ReadString('\n')
		partial.WriteString(line)

		if err != nil {
			time.Sleep(500 * time.Millisecond)
			return true
		}

		full := strings.TrimRight(partial.String(), "\n")
		partial.Reset()

		fmt.Fprintf(w, "data: %s\n\n", full)
		return true
	})
}

func RoomInfo(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var port int
	var admin, name string
	err = db.QueryRow(c.Request.Context(), "SELECT port, admin, name FROM rooms WHERE room_id = $1", roomId).Scan(&port, &admin, &name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"port": port, "admin": admin, "name": name})
}

func SendPatchFile(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	fileName := c.Param("filename")

	var extractFolderPath string
	err = db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	filePath := filepath.Join(extractFolderPath, fileName)

	c.FileAttachment(filePath, fileName)
}

func ChangeRoomName(c *gin.Context, db *pgxpool.Pool, processManager *pm.ProcessManager) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var admin string
	if err = db.QueryRow(c.Request.Context(), "SELECT admin FROM rooms WHERE room_id = $1", roomId).Scan(&admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	userValue, exists := c.Get("cshauth")
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Couldn't get logged in user"})
		return
	}

	user, ok := userValue.(*cshAuth.Claims)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Couldn't get logged in user"})
		return
	}

	if admin != user.Uuid {
		c.JSON(http.StatusForbidden, gin.H{"error": "You are not the admin of this room"})
		return
	}

	var data map[string]any

	if err = c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get room name from JSON: %s", err.Error())})
		return
	}

	name := data["name"].(string)
	if _, err = db.Exec(c.Request.Context(), "UPDATE rooms SET name = $1 WHERE room_id = $2", name, roomId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to update room name: %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success"})
}
