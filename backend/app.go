package main

import (
	"archive/zip"
	"bufio"
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Picture  string `json:"picture_url"`
	CSH      bool   `json:"csh"`
}

type Server struct {
	db             *pgxpool.Pool
	processManager *ProcessManager
}

var user User
var UPLOADS = "./uploads"
var SERVER_PORT = 38281
var PORT_RANGE = 20

//go:embed migrations/*.sql
var migrationsFS embed.FS

func NewServer(db *pgxpool.Pool, mngr *ProcessManager) *Server {
	return &Server{db: db, processManager: mngr}
}

func getUser(c *gin.Context) {
	c.JSON(http.StatusOK, user)
}

func (server *Server) uploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	privateStr := c.PostForm("private")
	private, err := strconv.ParseBool(privateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid boolean: " + err.Error()})
		return
	}

	if !strings.HasSuffix(file.Filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File must be a .zip file"})
		return
	}

	var port int

	// Generate ports and find an available one
	ports := rand.Perm(SERVER_PORT + PORT_RANGE - SERVER_PORT)
	for _, tryport := range ports {
		if checkPort(tryport) {
			port = tryport
			break
		}
	}

	if port == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not find an available port in range, try again later"})
		return
	}

	port += SERVER_PORT

	zipFolderPath := filepath.Join(UPLOADS, filepath.Base(file.Filename))
	extractFolderPath := zipFolderPath[:strings.Index(zipFolderPath, ".")]

	if _, err := os.Stat(extractFolderPath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Archipelago game with the same name already exists. Please change the name of your zip file."})
		return
	}
	c.SaveUploadedFile(file, zipFolderPath)

	roomId := uuid.New()
	admin := "5"
	start := time.Now()

	archFilePath := ""

	archive, err := zip.OpenReader(zipFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open zip"})
		return
	}
	defer archive.Close()

	for _, file := range archive.File {
		filePath := filepath.Join(extractFolderPath, file.Name)

		if strings.HasSuffix(filePath, ".archipelago") {
			archFilePath = filePath
		}
		if !strings.HasPrefix(filePath, filepath.Clean(extractFolderPath)+string(os.PathSeparator)) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid file path in zip"})
			return
		}
		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create parent directory"})
			return
		}
		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create file"})
			return
		}

		fileInArchive, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open zip entry"})
			return
		}

		if _, err := io.Copy(dstFile, fileInArchive); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file contents"})
			return
		}

		dstFile.Close()
		fileInArchive.Close()
	}

	os.Remove(zipFolderPath)

	if archFilePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago file found in zip"})
	}
	roomName := extractFolderPath[strings.Index(extractFolderPath, "/")+1:]

	decodedArch, err := decompressAP(archFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err})
		return
	}

	ids := make(map[string]IdsToNames)
	for game, subdict := range decodedArch.Datapackage {
		ids[game] = *newIdsToNames()
		for k, v := range subdict.ItemNameToId {
			ids[game].IdToItemName[v] = k
		}
		for k, v := range subdict.LocationNameToId {
			ids[game].IdToLocationName[v] = k
		}
	}

	_, err = server.db.Exec(c.Request.Context(), "INSERT INTO rooms (room_id, port, admin, extract_folder_path, arch_file_path, start, name, private) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		roomId, port, admin, extractFolderPath, archFilePath, start, roomName, private)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert room into database: " + err.Error()})
		return
	}

	var locationRows [][]any
	for sphereNum, sphere := range decodedArch.Spheres {
		for slot, locations := range sphere {
			for _, locationId := range locations {
				slotName := decodedArch.SlotInfo[slot].SlotName
				slotGame := decodedArch.SlotInfo[slot].Game

				locationTuple := decodedArch.Locations[slot][strconv.Itoa(locationId)] // format is: [item_id, receiver_slot_id, unknown#]

				toName := decodedArch.SlotInfo[strconv.Itoa(locationTuple[1])].SlotName
				locationName, ok := ids[slotGame].IdToLocationName[locationId]
				if !ok {
					locationName = "Unknown"
				}
				itemName, ok := ids[decodedArch.SlotInfo[strconv.Itoa(locationTuple[1])].Game].IdToItemName[locationTuple[0]]
				if !ok {
					itemName = "Unknown"
				}
				row := []any{slot, strconv.Itoa(locationId), sphereNum + 1, slotName, slotGame, toName, locationName, itemName, roomId}
				locationRows = append(locationRows, row)
			}
		}
	}
	if _, err = server.db.CopyFrom(
		c.Request.Context(),
		pgx.Identifier{"locations"},
		[]string{"slot", "location_id", "sphere", "from_name", "game", "to_name", "location_name", "item_name", "room_id"},
		pgx.CopyFromRows(locationRows),
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert locations into database: " + err.Error()})
		return
	}

	var slots [][]any
	for slot, slotInfo := range decodedArch.SlotInfo {
		slots = append(slots, []any{slot, slotInfo.SlotName, slotInfo.Game, roomId})
	}
	if _, err = server.db.CopyFrom(
		c.Request.Context(),
		pgx.Identifier{"slots"},
		[]string{"id", "name", "game", "room_id"},
		pgx.CopyFromRows(slots),
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert slots into database: " + err.Error()})
		return
	}

	var items [][]any
	for game, idsToName := range ids {
		for itemId, itemName := range idsToName.IdToItemName {
			items = append(items, []any{game, itemName, strconv.Itoa(itemId), roomId})
		}
	}
	if _, err = server.db.CopyFrom(
		c.Request.Context(),
		pgx.Identifier{"items"},
		[]string{"game", "name", "id", "room_id"},
		pgx.CopyFromRows(items),
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert items into database: " + err.Error()})
		return
	}

	if err = server.processManager.StartServer(roomId, archFilePath, extractFolderPath, port); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start archipelago server: %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Server started",
		"port":    port,
		"room_id": roomId,
	})
}

func (server *Server) getAllRooms(c *gin.Context) {
	var timeFormat TimeFormat
	if err := c.ShouldBindJSON(&timeFormat); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	timeZone := timeFormat.Data.TimeZone

	// var rooms []map[string]any
	rooms := make([]map[string]any, 0)

	var start time.Time
	var port int
	var roomId, admin, name string
	rows, _ := server.db.Query(c.Request.Context(), "SELECT room_id, port, start, admin, name FROM rooms WHERE port >= $1 AND port < $2 AND active = true AND private = false", SERVER_PORT, SERVER_PORT+PORT_RANGE)
	_, err := pgx.ForEachRow(rows, []any{&roomId, &port, &start, &admin, &name}, func() error {
		room := make(map[string]any)
		room["room_id"] = roomId
		room["port"] = port
		startStr, formatErr := formatRoomTime(start, timeZone)
		if formatErr != nil {
			return formatErr
		}
		room["start"] = startStr
		room["start_for_sorting"] = start
		room["admin_uuid"] = admin
		room["name"] = name
		roomUUID, err := uuid.Parse(roomId)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to interpret room Id as uuid: %s", err.Error())})
		}
		room["running"] = server.processManager.IsRunning(roomUUID)

		rooms = append(rooms, room)
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to format start date: %s", err.Error())})
		return
	}

	// Sort list based on starting time
	slices.SortFunc(rooms, func(a, b map[string]any) int {
		A := a["start_for_sorting"].(time.Time)
		B := b["start_for_sorting"].(time.Time)
		return -time.Time.Compare(A, B)
	})

	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

func (server *Server) deleteRoom(c *gin.Context) {
	roomId := c.Param("roomId")

	var admin, extractFolderPath string
	err := server.db.QueryRow(c.Request.Context(), "SELECT admin, extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&admin, &extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get info from database: %s", err.Error())})
		return
	}

	// check if current user is admin

	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to parse room Id: %s", err.Error())})
		return
	}
	server.processManager.Terminate(roomUUID)

	err = os.RemoveAll(extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to delete room folder: %s", err.Error())})
		return
	}

	server.db.Exec(c.Request.Context(), "DELETE FROM locations WHERE room_id = $1", roomId)
	server.db.Exec(c.Request.Context(), "DELETE FROM items WHERE room_id = $1", roomId)
	server.db.Exec(c.Request.Context(), "UPDATE rooms SET active = false WHERE room_id = $1", roomId)
	server.db.Exec(c.Request.Context(), "DELETE FROM slots WHERE player_uuid is null AND room_id IN (SELECT room_id FROM rooms WHERE active = false)")
	server.db.Exec(c.Request.Context(), "DELETE FROM rooms WHERE room_id NOT IN (SELECT room_id FROM slots)")

	c.JSON(http.StatusOK, gin.H{"message": "successfully deleted"})
}

func (server *Server) restartRoom(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	var archFilePath, extractFolderPath string
	var oldport int
	err = server.db.QueryRow(c.Request.Context(), "SELECT arch_file_path, extract_folder_path, port FROM rooms WHERE room_id = $1", roomId).Scan(&archFilePath, &extractFolderPath, &oldport)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get info from database: %s", err.Error())})
		return
	}

	if _, err := os.Stat(archFilePath); errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("No archipelago file at path: %s", err.Error())})
		return
	}

	if !server.processManager.IsRunning(roomUUID) {
		if restarting, _ := server.processManager.IsRestarting(roomUUID); restarting {
			c.JSON(http.StatusNoContent, gin.H{"error": "Server already restarting"})
			return
		}
		server.processManager.SetRestarting(roomUUID, true)

		var port int
		// Generate ports and find an available one
		ports := rand.Perm(SERVER_PORT + PORT_RANGE - 1 - SERVER_PORT)
		ports = append([]int{oldport - SERVER_PORT}, ports...)
		for _, tryport := range ports {
			if checkPort(tryport) {
				port = tryport
				break
			}
		}

		if port == 0 {
			server.processManager.SetRestarting(roomUUID, false)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not find an available port in range, try again later"})
			return
		}

		port += SERVER_PORT

		if port != oldport {
			_, err = server.db.Exec(c.Request.Context(), "UPDATE rooms SET port = $1 WHERE room_id = $2", port, roomId)
			if err != nil {
				server.processManager.SetRestarting(roomUUID, false)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update port in database: " + err.Error()})
				return
			}
		}

		if err = server.processManager.StartServer(roomUUID, archFilePath, extractFolderPath, port); err != nil {
			server.processManager.SetRestarting(roomUUID, false)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to restart archipelago server: %s", err.Error())})
			return
		}

		server.processManager.SetRestarting(roomUUID, false)

		c.JSON(http.StatusOK, gin.H{"message": "Server restarted", "port": port})
	} else {
		c.JSON(http.StatusNoContent, gin.H{"error": "Server already running"})
		return
	}
}

func (server *Server) getLog(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var extractFolderPath string
	err = server.db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	logFile := extractFolderPath + "/server-log.txt"
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

func (server *Server) serverCommand(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	if !server.processManager.IsRunning(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Archipelago server not running"})
		return
	}

	var admin string
	err = server.db.QueryRow(c.Request.Context(), "SELECT admin FROM rooms WHERE room_id = $1", roomId).Scan(&admin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	// check if the user is the admin

	var data map[string]any

	if err = c.BindJSON(&data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get command from JSON: %s", err.Error())})
		return
	}

	command := data["command"].(string)
	err = server.processManager.SendCommand(roomUUID, command)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to send command to server: %s", err.Error())})
		return
	}

	if args := strings.Split(command, " "); len(args) >= 2 && strings.HasPrefix(command, "/release") {
		gameName := strings.TrimSpace(strings.ToLower(command[strings.Index(command, " "):]))
		server.db.Exec(c.Request.Context(), "INSERT INTO released_games VALUES ($1, $2)", gameName, roomId)
	}
}

func (server *Server) streamLog(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var extractFolderPath string
	err = server.db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	logFile := extractFolderPath + "/server-log.txt"
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

func (server *Server) roomInfo(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var port int
	var admin, name string
	err = server.db.QueryRow(c.Request.Context(), "SELECT port, admin, name FROM rooms WHERE room_id = $1", roomId).Scan(&port, &admin, &name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"port": port, "admin": admin, "name": name})
}

func (server *Server) getPlayers(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var archFilePath, extractFolderPath string
	err = server.db.QueryRow(c.Request.Context(), "SELECT arch_file_path, extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&archFilePath, &extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	players, err := getPlayerInfo(archFilePath, extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slices.SortFunc(players, func(a, b map[string]any) int {
		A := a["slot"].(int)
		B := b["slot"].(int)
		return cmp.Compare(A, B)
	})

	c.JSON(http.StatusOK, gin.H{"players": players})
}

func (server *Server) sendPatchFile(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	fileName := c.Param("filename")

	var extractFolderPath string
	err = server.db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	filePath := filepath.Join(extractFolderPath, fileName)

	c.FileAttachment(filePath, fileName)
}

func (s *SlotInfo) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) < 4 {
		return fmt.Errorf("slot_info entry: expected 4 elements, got %d", len(raw))
	}

	if err := json.Unmarshal(raw[0], &s.SlotName); err != nil {
		return fmt.Errorf("slot_info name: %w", err)
	}
	if err := json.Unmarshal(raw[1], &s.Game); err != nil {
		return fmt.Errorf("slot_info game: %w", err)
	}
	return nil
}

func runMigrations(databaseURL string) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, databaseURL+"?sslmode=disable")
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func main() {
	user.UUID = "5"
	user.Username = "lung"
	user.Picture = "https://profiles.csh.rit.edu/image/lung"
	user.CSH = true

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	processManager := NewProcessManager()

	server := NewServer(pool, processManager)
	if err := runMigrations(os.Getenv("DATABASE_URL")); err != nil {
		log.Fatal(err)
	}

	// Restart rooms currently in database
	var oldport int
	var roomId, extractFolderPath, archFilePath string
	rows, _ := server.db.Query(context.Background(), "SELECT room_id, port, extract_folder_path, arch_file_path FROM rooms WHERE port >= $1 AND port < $2 AND active = true", SERVER_PORT, SERVER_PORT+PORT_RANGE)
	_, err = pgx.ForEachRow(rows, []any{&roomId, &oldport, &extractFolderPath, &archFilePath}, func() error {
		roomUUID, err := uuid.Parse(roomId)
		if err != nil {
			return fmt.Errorf("Failed to parse room id to uuid: %s", err.Error())
		}

		var port int
		// Generate ports and find an available one
		ports := rand.Perm(SERVER_PORT + PORT_RANGE - 1 - SERVER_PORT)
		ports = append([]int{oldport - SERVER_PORT}, ports...)
		for _, tryport := range ports {
			if checkPort(tryport) {
				port = tryport
				break
			}
		}

		if port == 0 {
			return fmt.Errorf("Could not find an available port for room %s", roomId)
		}

		port += SERVER_PORT

		if port != oldport {
			_, err = server.db.Exec(context.Background(), "UPDATE rooms SET port = $1 WHERE room_id = $2", port, roomId)
			if err != nil {
				return fmt.Errorf("failed to update port in database: %s", err.Error())
			}
		}

		if err = server.processManager.StartServer(roomUUID, archFilePath, extractFolderPath, port); err != nil {
			return fmt.Errorf("failed to restart archipelago server: %s", err.Error())
		}

		return nil
	})

	os.Mkdir(filepath.Join(".", "uploads"), os.ModePerm)

	router := gin.Default()
	router.SetTrustedProxies(nil)

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/api/user", getUser)
	router.POST("/api/upload", server.uploadFile)
	router.PUT("/api/rooms", server.getAllRooms)
	router.DELETE("/api/delete/:roomId", server.deleteRoom)
	router.GET("/api/room/:roomId", server.roomInfo)
	router.GET("/api/players/:roomId", server.getPlayers)
	router.GET("/api/players/:roomId/:filename", server.sendPatchFile)
	router.PUT("/api/restart/:roomId", server.restartRoom)
	router.GET("/api/log/:roomId", server.getLog)
	router.GET("/api/log/stream/:roomId", server.streamLog)
	router.POST("/api/command/:roomId", server.serverCommand)

	router.Run(":5001")
}
