package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

func getPlayerInfo(archFilePath string, extractFolderPath string) ([]map[string]string, error) {
	decodedArch, err := decompressAP(archFilePath)
	if err != nil {
		return []map[string]string{}, fmt.Errorf("Failed to decompress archipelago file: %w", err)
	}

	var players []map[string]string
	for slotId, slotInfo := range decodedArch.SlotInfo {
		players = append(players, map[string]string{"slot": slotId, "name": slotInfo.SlotName, "game": slotInfo.Game})
	}

	files, err := os.ReadDir(extractFolderPath)
	if err != nil {
		return []map[string]string{}, fmt.Errorf("Failed to read extract folder: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() {
			if PIndex := strings.Index(file.Name()[2:], "P"); PIndex != -1 {
				var patchId strings.Builder
				for _, letter := range file.Name()[PIndex+1:] {
					if !unicode.IsDigit(letter) {
						break
					}
					patchId.WriteString(string(letter))
				}

				for _, player := range players {
					if player["slot"] == patchId.String() {
						player["patch"] = file.Name()
					}
				}
			}
		}
	}

	return players, nil
}
