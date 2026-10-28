package main

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"uuid"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
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
	rows, _ := server.db.Query(c.Request.Context(), "SELECT name FROM released_games WHERE room_id = $1", roomId)
	releasedGames, err := pgx.CollectRows(rows, pgx.RowTo[string])
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
	hintList := make([]map[string]any, 0)

	totalChecks := 0
	totalChecked := 0
	gamesComplete := 0
	recentActivity := "None"
	recentActivityDt := time.Since(time.Unix(0, 0))

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

				playerActivities := make(map[PlayerTuple]float64)
				for _, clientActivityTimer := range decodedApsave.ClientActivityTimers {
					playerActivities[clientActivityTimer.PlayerTuple] = clientActivityTimer.Timer
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

					if playerActivity, exists := playerActivities[playerTuple]; exists {
						timeStamp := time.Unix(int64(playerActivity), 0)
						timeDiff := time.Since(timeStamp)

						hours := timeDiff / time.Hour
						minutes := (timeDiff % time.Hour) / time.Minute

						player["last_activity"] = fmt.Sprintf("%02d:%02d", hours, minutes)
						player["last_activity_num"] = timeDiff.Seconds()

						if recentActivityDt > timeDiff {
							recentActivity = fmt.Sprintf("%02d:%02d", hours, minutes)
							recentActivityDt = timeDiff
						}
					} else {
						player["last_activity"] = "None"
						player["last_activity_num"] = math.MaxInt
					}

					if status, exists := decodedApsave.ClientGameState[playerTuple]; exists {
						player["status"] = status
						if status == 30 {
							gamesComplete += 1
						} else if slices.Contains(releasedGames, strings.ToLower(player["name"].(string))) {
							player["status"] = 25
						}
					} else {
						if slices.Contains(releasedGames, strings.ToLower(player["name"].(string))) {
							player["status"] = 25
						} else {
							player["status"] = 0
						}
					}
				}

				var hintQueries pgx.Batch
				for player, hints := range decodedApsave.Hints {
					slot := player.Slot
					for _, hintInfo := range hints {
						if hintInfo.FindingPlayer == slot {
							queuedQuery := hintQueries.Queue("SELECT location_name, to_name, from_name, item_name, game FROM locations WHERE room_id = $1 AND slot = $2 AND location_id = $3", roomId, slot, strconv.Itoa(hintInfo.Location))
							queuedQuery.QueryRow(func(row pgx.Row) error {
								var locationName, toName, fromName, itemName, game string
								if err := row.Scan(&locationName, &toName, &fromName, &itemName, &game); err != nil {
									return err
								}

								hint := make(map[string]any)

								hint["location"] = locationName
								hint["receiving_player"] = toName
								hint["finding_player"] = fromName
								hint["item"] = itemName
								hint["game"] = game
								if hintInfo.Entrance == "" {
									hint["entrance"] = "Vanilla"
								} else {
									hint["entrance"] = hintInfo.Entrance
								}
								hint["found"] = hintInfo.Found

								hintList = append(hintList, hint)

								return nil
							})

						}
					}
				}

				batchResult := server.db.SendBatch(c.Request.Context(), &hintQueries)
				if err = batchResult.Close(); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to scan hint data from database row: %s", err.Error())})
					return
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
			player["last_activity_num"] = math.MaxInt
			player["status"] = 0
			player["percent_checked"] = 0
		}
	}

	totals := map[string]any{
		"total_checks":             totalChecks,
		"total_checked":            totalChecked,
		"games_complete":           gamesComplete,
		"num_players":              len(players),
		"num_players_not_released": len(players) - len(releasedGames),
		"recent_activity":          recentActivity,
	}

	c.JSON(http.StatusOK, gin.H{
		"players": players,
		"totals":  totals,
		"hints":   hintList,
		"port":    port,
	})
}

func (server *Server) individualPlayerData(c *gin.Context) {
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

	slot := c.Param("slot")

	var extractFolderPath string
	err = server.db.QueryRow(c.Request.Context(), "SELECT extract_folder_path FROM rooms WHERE room_id = $1", roomId).Scan(&extractFolderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}

	decodedArch, err := server.processManager.GetArchipelagoFile(roomUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get archipelago file: %s", err.Error())})
		return
	}

	items := make(map[string]map[string]any, 0)
	locations := make([]map[string]any, 0)
	hintList := make([]map[string]any, 0)
	var name string
	var playerUuid sql.NullString

	var locationName, locationId string
	locationRows, err := server.db.Query(c.Request.Context(), "SELECT location_name, location_id FROM locations WHERE room_id = $1 AND slot = $2", roomId, slot)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get information from database: %s", err.Error())})
		return
	}
	_, err = pgx.ForEachRow(locationRows, []any{&locationName, &locationId}, func() error {
		location := make(map[string]any)
		location["name"] = locationName
		location["checked"] = false
		location["number"] = locationId
		locations = append(locations, location)
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to put locations into list: %s", err.Error())})
		return
	}

	itemInfos := make(map[string]map[string]string)
	var itemid, itemname string
	var locationsChecked int
	itemRows, _ := server.db.Query(c.Request.Context(), "SELECT i.id, i.name, s.name, s.player_uuid, s.checks FROM items as i, slots as s WHERE i.room_id = $1 AND s.room_id = i.room_id AND s.id = $2 AND i.game = s.game", roomId, slot)
	_, err = pgx.ForEachRow(itemRows, []any{&itemid, &itemname, &name, &playerUuid, &locationsChecked}, func() error {
		itemInfos[itemid] = make(map[string]string)
		itemInfos[itemid]["name"] = itemname
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to put precollected items into list: %s", err.Error())})
		return
	}

	for index, itemid := range decodedArch.PrecollectedItems[slot] {
		itemidstr := strconv.Itoa(itemid)
		itemName := itemInfos[itemidstr]["name"]
		if _, exists := items[itemidstr]; exists {
			items[itemName]["count"] = items[itemidstr]["count"].(int) + 1
		} else {
			items[itemName] = make(map[string]any)
			items[itemName]["count"] = 1
		}
		items[itemName]["last_order_received"] = index + 1
	}

	// files, err := os.ReadDir(extractFolderPath)
	// if err != nil {
	// 	c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read extract folder: %s", err.Error())})
	// 	return
	// }
	// for _, file := range files {
	// 	if !file.IsDir() {
	// 		if strings.HasSuffix(file.Name(), ".apsave") {
	// 			// decompress apsave
	// 			decodedApsave, err := decompressAPSave(filepath.Join(extractFolderPath, file.Name()))
	// 			if err != nil {
	// 				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to decompress apsave file: %s", err.Error())})
	// 				return
	// 			}
	// 		}
	// 	}
	// }

	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"locations": locations,
		"hints":     hintList,
		"name":      name,
		"uuid":      playerUuid,
	})
}
