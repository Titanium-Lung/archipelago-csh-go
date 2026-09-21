package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

func getPlayerInfo(archFilePath string, extractFolderPath string) ([]map[string]any, error) {
	decodedArch, err := decompressAP(archFilePath)
	if err != nil {
		return []map[string]any{}, fmt.Errorf("Failed to decompress archipelago file: %w", err)
	}

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
			} else {
				fmt.Println(PIndex)
			}
		}
	}

	return players, nil
}
