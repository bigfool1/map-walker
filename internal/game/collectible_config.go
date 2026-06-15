package game

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

// CollectibleRegion 是一个收集品区域配置
type CollectibleRegion struct {
	ID           string
	CenterLat    float64
	CenterLng    float64
	RadiusMeters float64
	TargetCount  int
	RespawnMin   time.Duration
	RespawnMax   time.Duration
}

type collectibleRegionJSON struct {
	ID                string             `json:"id"`
	Center            collectiblePointJSON `json:"center"`
	RadiusMeters      float64            `json:"radiusMeters"`
	TargetCount       int                `json:"targetCount"`
	RespawnMinSeconds int                `json:"respawnMinSeconds"`
	RespawnMaxSeconds int                `json:"respawnMaxSeconds"`
}

type collectiblePointJSON struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type collectibleRegionsFile struct {
	Regions []collectibleRegionJSON `json:"regions"`
}

// LoadCollectibleRegions 加载并验证收集品区域配置
func LoadCollectibleRegions(path string) ([]CollectibleRegion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取区域配置文件失败: %w", err)
	}

	var file collectibleRegionsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析区域配置 JSON 失败: %w", err)
	}

	regions, err := validateAndConvertRegions(file.Regions)
	if err != nil {
		return nil, err
	}

	return regions, nil
}

func validateAndConvertRegions(jsons []collectibleRegionJSON) ([]CollectibleRegion, error) {
	if len(jsons) != 3 {
		return nil, fmt.Errorf("区域配置需要恰好 3 个区域，实际: %d", len(jsons))
	}

	ids := make(map[string]struct{}, len(jsons))
	regions := make([]CollectibleRegion, 0, len(jsons))

	for _, j := range jsons {
		if j.ID == "" {
			return nil, fmt.Errorf("区域 ID 不能为空")
		}
		if _, dup := ids[j.ID]; dup {
			return nil, fmt.Errorf("区域 ID 重复: %s", j.ID)
		}
		ids[j.ID] = struct{}{}

		if j.Center.Lat < -90 || j.Center.Lat > 90 {
			return nil, fmt.Errorf("区域 %s 纬度 %v 无效", j.ID, j.Center.Lat)
		}
		if j.Center.Lng < -180 || j.Center.Lng > 180 {
			return nil, fmt.Errorf("区域 %s 经度 %v 无效", j.ID, j.Center.Lng)
		}
		if j.RadiusMeters <= 0 {
			return nil, fmt.Errorf("区域 %s 半径 %v 无效，必须 > 0", j.ID, j.RadiusMeters)
		}
		if j.TargetCount <= 0 {
			return nil, fmt.Errorf("区域 %s targetCount %d 无效，必须 > 0", j.ID, j.TargetCount)
		}
		if j.RespawnMinSeconds <= 0 || j.RespawnMaxSeconds <= 0 {
			return nil, fmt.Errorf("区域 %s 重生时间必须 > 0", j.ID)
		}
		if j.RespawnMinSeconds > j.RespawnMaxSeconds {
			return nil, fmt.Errorf("区域 %s 最小重生时间(%ds) > 最大重生时间(%ds)", j.ID, j.RespawnMinSeconds, j.RespawnMaxSeconds)
		}

		regions = append(regions, CollectibleRegion{
			ID:           j.ID,
			CenterLat:    j.Center.Lat,
			CenterLng:    j.Center.Lng,
			RadiusMeters: j.RadiusMeters,
			TargetCount:  j.TargetCount,
			RespawnMin:   time.Duration(j.RespawnMinSeconds) * time.Second,
			RespawnMax:   time.Duration(j.RespawnMaxSeconds) * time.Second,
		})
	}

	if err := checkRegionOverlap(regions); err != nil {
		return nil, err
	}

	return regions, nil
}

func checkRegionOverlap(regions []CollectibleRegion) error {
	for i := 0; i < len(regions); i++ {
		for j := i + 1; j < len(regions); j++ {
			a, b := regions[i], regions[j]
			dist := haversineMeters(a.CenterLat, a.CenterLng, b.CenterLat, b.CenterLng)
			minDist := a.RadiusMeters + b.RadiusMeters
			if dist < minDist {
				return fmt.Errorf("区域 %s 与 %s 重叠 (距离 %.0fm < 最小间距 %.0fm)", a.ID, b.ID, dist, minDist)
			}
		}
	}
	return nil
}

func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	// 对于上海区域的小距离（< 2km），简化的平面近似足够准确
	dlat := (lat1 - lat2) * metersPerDegreeLatitude
	dlng := (lng1 - lng2) * metersPerDegreeLongitude((lat1+lat2)/2)
	return math.Hypot(dlat, dlng)
}
