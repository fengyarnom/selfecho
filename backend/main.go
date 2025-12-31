package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

type healthPayload struct {
	CPUPercent float64 `json:"cpuPercent"`
	TotalMem   uint64  `json:"totalMemBytes"`
	UsedMem    uint64  `json:"usedMemBytes"`
	DiskTotal  uint64  `json:"diskTotalBytes"`
	DiskUsed   uint64  `json:"diskUsedBytes"`
}

func main() {
	router := gin.Default()

	router.GET("/api/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "hello from backend"})
	})

	router.GET("/health", func(c *gin.Context) {
		cpuPercent := 0.0
		if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
			cpuPercent = percents[0]
		}

		memStats, memErr := mem.VirtualMemory()
		diskStats, diskErr := disk.Usage("/")

		if memErr != nil || diskErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to read system metrics"})
			return
		}

		c.JSON(http.StatusOK, healthPayload{
			CPUPercent: cpuPercent,
			TotalMem:   memStats.Total,
			UsedMem:    memStats.Used,
			DiskTotal:  diskStats.Total,
			DiskUsed:   diskStats.Used,
		})
	})

	router.Run(":8080")
}
