package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"uuid"

	"github.com/gin-gonic/gin"
)

func GetPlayerInfo(decodedArch *ArchipelagoFile, extractFolderPath string) ([]map[string]any, error) {
	// decodedArch, err := decompressAP(archFilePath)
	// if err != nil {
	// 	return []map[string]any{}, fmt.Errorf("Failed to decompress archipelago file: %w", err)
	// }

	var players []map[string]any
	for slotId, slotInfo := range decodedArch.SlotInfo {
		slot, err := strconv.Atoi(slotId)
		if err != nil {
			return []map[string]any{}, fmt.Errorf("Invalid slot integer: %w", err)
		}
		players = append(players, map[string]any{"slot": slot, "name": slotInfo.SlotName, "game": slotInfo.Game})
	}

	files, err := os.ReadDir(extractFolderPath)
	if err != nil {
		return []map[string]any{}, fmt.Errorf("Failed to read extract folder: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() {
			if PIndex := strings.Index(file.Name()[2:], "P"); PIndex != -1 {
				var patchIdStr strings.Builder
				for _, letter := range file.Name()[PIndex+3:] {
					if !unicode.IsDigit(letter) {
						break
					}
					patchIdStr.WriteString(string(letter))
				}

				patchId, err := strconv.Atoi(patchIdStr.String())
				if err != nil {
					return []map[string]any{}, fmt.Errorf("Failed to convert patch id to int (even tho it's literally impossible to not): %w", err)
				}
				for _, player := range players {
					if player["slot"].(int) == patchId {
						player["patch"] = file.Name()
					}
				}
			}
		}
	}

	return players, nil
}

func (server *Server) multiworldData(c *gin.Context) {
	roomId := c.Param("roomId")
	roomUUID, err := uuid.Parse(roomId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse room id to uuid: %s", err.Error())})
		return
	}

	if !server.processManager.Exists(roomUUID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No archipelago game with this id"})
		return
	}

	var extractFolderPath string
	var port int
	err = server.db.QueryRow(c.Request.Context(), "SELECT extract_folder_path, port FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath, &port)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}
	// rows, _ := server.db.Query(c.Request.Context(), "SELECT name FROM released_games WHERE room_id = $1", roomId)
	// releasedGames, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	decodedArch, err := server.processManager.GetArchipelagoFile(roomUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get archipelago file: %s", err.Error())})
		return
	}

	var players []map[string]any
	for slotId, slotInfo := range decodedArch.SlotInfo {
		slot, err := strconv.Atoi(slotId)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Invalid slot integer: %s", err.Error())})
			return
		}
		players = append(players, map[string]any{"slot": slot, "name": slotInfo.SlotName, "game": slotInfo.Game})
	}

	// var hints []map[string]string
	hints := make([]map[string]string, 0)

	totalChecks := 0
	totalChecked := 0
	gamesComplete := 0
	recentActivity := "None"
	// recentActivityDt := time.Now().Sub(time.Unix(0, 0))

	files, err := os.ReadDir(extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read extract folder: %s", err.Error())})
		return
	}
	apsave := false
	for _, file := range files {
		if !file.IsDir() {
			if strings.HasSuffix(file.Name(), ".apsave") {
				// decompress apsave
				decodedApsave, err := decompressAPSave(filepath.Join(extractFolderPath, file.Name()))
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to decompress apsave file: %s", err.Error())})
					return
				}

				apsave = true

				playerActivity := make(map[PlayerTuple]float64)
				for _, clientActivityTimer := range decodedApsave.ClientActivityTimers {
					playerActivity[clientActivityTimer.PlayerTuple] = clientActivityTimer.Timer
				}

				for _, player := range players {
					checks := len(decodedArch.Locations[strconv.Itoa(player["slot"].(int))])
					player["total_checks"] = checks
					totalChecks += checks

					playerTupleList := decodedArch.ConnectNames[player["name"].(string)] // format: [team#, slot#]
					playerTuple := PlayerTuple{Team: playerTupleList[0], Slot: playerTupleList[1]}

					var checked int
					locationChecks, exists := decodedApsave.LocationChecks[playerTuple]
					if !exists {
						checked = 0
					} else {
						checked = len(locationChecks)
					}
					player["checks_found"] = checked
					totalChecked += checked

					player["percent_checked"] = float64(checked) / float64(checks) * 100

					// Temporary
					player["last_activity"] = "None"
					player["last_activity_num"] = 1000000
					player["status"] = 0
				}
			}
		}
	}
	if !apsave {
		for _, player := range players {
			checks := len(decodedArch.Locations[strconv.Itoa(player["slot"].(int))])
			player["total_checks"] = checks
			totalChecks += checks

			player["checks_found"] = 0
			player["last_activity"] = "None"
			player["last_activity_num"] = 1000000
			player["status"] = 0
			player["percent_checked"] = 0
		}
	}

	totals := map[string]any{
		"total_checks":             totalChecks,
		"total_checked":            totalChecked,
		"games_complete":           gamesComplete,
		"num_players":              len(players),
		"num_players_not_released": len(players),
		"recent_activity":          recentActivity,
	}

	c.JSON(http.StatusOK, gin.H{
		"players": players,
		"totals":  totals,
		"hints":   hints,
		"port":    port,
	})
}
