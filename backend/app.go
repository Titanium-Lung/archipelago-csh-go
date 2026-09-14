package main

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

type Pool struct {
	db *pgxpool.Pool
}

var user User
var UPLOADS = "./uploads"
var SERVER_PORT = 38281
var PORT_RANGE = 20

//go:embed migrations/*.sql
var migrationsFS embed.FS

func NewPool(db *pgxpool.Pool) *Pool {
	return &Pool{db: db}
}

func getUser(c *gin.Context) {
	c.JSON(http.StatusOK, user)
}

func (pool *Pool) uploadFile(c *gin.Context) {
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
		panic(err)
	}
	defer archive.Close()

	for _, file := range archive.File {
		filePath := filepath.Join(extractFolderPath, file.Name)

		if strings.HasSuffix(filePath, ".archipelago") {
			archFilePath = filePath
		}
		if !strings.HasPrefix(filePath, filepath.Clean(extractFolderPath)+string(os.PathSeparator)) {
			fmt.Println("invalid file path")
			return
		}
		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
			panic(err)
		}
		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			panic(err)
		}

		fileInArchive, err := file.Open()
		if err != nil {
			panic(err)
		}

		if _, err := io.Copy(dstFile, fileInArchive); err != nil {
			panic(err)
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

	_, err = pool.db.Exec(c.Request.Context(), "INSERT INTO rooms (room_id, port, admin, extract_folder_path, arch_file_path, start, name, private) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
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
	if _, err = pool.db.CopyFrom(
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
	if _, err = pool.db.CopyFrom(
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
	if _, err = pool.db.CopyFrom(
		c.Request.Context(),
		pgx.Identifier{"items"},
		[]string{"game", "name", "id", "room_id"},
		pgx.CopyFromRows(items),
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert items into database: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Server started",
		"port":    port,
		"room_id": roomId,
	})
}

func checkPort(port int) bool {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

func decompressAP(apPath string) (ArchipelagoFile, error) {
	cmd := exec.Command("python3", "decompress_ap.py", apPath)
	if dir := os.Getenv("ARCHIPELAGO_SCRIPTS_DIR"); dir != "" {
		cmd.Dir = dir
	} else {
		cmd.Dir = "./"
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var result ArchipelagoFile
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("python script failed: %w, stderr: %s", err, stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("failed to parse python output as JSON: %w, raw output: %s", err, stdout.String())
	}

	return result, nil
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

	dbpool := NewPool(pool)
	if err := runMigrations(os.Getenv("DATABASE_URL")); err != nil {
		log.Fatal(err)
	}

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
	router.POST("/api/upload", dbpool.uploadFile)

	router.Run(":5001")
}
