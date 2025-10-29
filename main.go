package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/AnkitNayan83/live-user-count/service"
	"github.com/AnkitNayan83/live-user-count/utils"
	"github.com/gin-gonic/gin"
)

func main() {
	fmt.Println("-------------------------Live User Count Service-------------------------")
	config, err := utils.LoadConfig(".")
	if err != nil {
		log.Fatal("cannot load config:", err)
	}

	redisURL := config.RedisUrl
	redisPassword := config.RedisPassword

	if redisURL == "" {
		log.Fatal("REDIS_URL env var not set")
	}

	if redisPassword == "" {
		log.Fatal("REDIS_PASSWORD env var not set")
	}

	// init redis client and hub
	service.InitRedis(redisURL, redisPassword)
	h := service.NewHub()
	go h.Run()                  // hub main loop
	go service.StartRedisSub(h) // subscribe to redis pubsub for cross-instance broadcasts

	r := gin.Default()

	// static html file
	r.GET("/", func(c *gin.Context) {
		c.File("./test/index.html")
	})

	// simple health
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// REST: get current count for a page
	r.GET("/count/:page", func(c *gin.Context) {
		pageID := c.Param("page")
		cnt, _ := service.GetCount(pageID)
		c.JSON(http.StatusOK, gin.H{"page": pageID, "count": cnt})
	})

	// websocket endpoint — clients connect here
	r.GET("/ws", func(c *gin.Context) {
		service.ServeWs(h, c.Writer, c.Request)
	})

	addr := config.Port
	if addr == "" {
		log.Fatal("PORT env var not set")
	}
	log.Println("listening on", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
