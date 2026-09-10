package main

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type User struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Picture  string `json:"picture_url"`
	CSH      bool   `json:"csh"`
}

var user User

func getUser(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, user)
}

func main() {
	user.UUID = "5"
	user.Username = "lung"
	user.Picture = "https://profiles.csh.rit.edu/image/lung"
	user.CSH = true

	router := gin.Default()

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/api/user", getUser)

	router.Run(":5001")
}
