package game

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validRegionsJSON() []byte {
	return []byte(`{
  "regions": [
    {
      "id": "region-1",
      "center": {"lat": 31.2304, "lng": 121.4737},
      "radiusMeters": 200,
      "targetCount": 5,
      "respawnMinSeconds": 5,
      "respawnMaxSeconds": 15
    },
    {
      "id": "region-2",
      "center": {"lat": 31.2350, "lng": 121.4780},
      "radiusMeters": 200,
      "targetCount": 5,
      "respawnMinSeconds": 5,
      "respawnMaxSeconds": 15
    },
    {
      "id": "region-3",
      "center": {"lat": 31.2270, "lng": 121.4700},
      "radiusMeters": 200,
      "targetCount": 5,
      "respawnMinSeconds": 5,
      "respawnMaxSeconds": 15
    }
  ]
}`)
}

func writeTempConfig(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "regions.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeTempConfig(t, validRegionsJSON())
	regions, err := LoadCollectibleRegions(path)
	if err != nil {
		t.Fatalf("加载有效配置失败: %v", err)
	}
	if len(regions) != 3 {
		t.Fatalf("区域数 = %d, want 3", len(regions))
	}
	for i, r := range regions {
		if r.ID == "" {
			t.Fatalf("region[%d] ID 为空", i)
		}
		if r.TargetCount != 5 {
			t.Fatalf("region[%d] TargetCount = %d, want 5", i, r.TargetCount)
		}
		if r.RadiusMeters != 200 {
			t.Fatalf("region[%d] RadiusMeters = %v, want 200", i, r.RadiusMeters)
		}
		if r.RespawnMin != 5*time.Second {
			t.Fatalf("region[%d] RespawnMin = %v, want 5s", i, r.RespawnMin)
		}
		if r.RespawnMax != 15*time.Second {
			t.Fatalf("region[%d] RespawnMax = %v, want 15s", i, r.RespawnMax)
		}
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadCollectibleRegions("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("期望文件未找到错误")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := writeTempConfig(t, []byte(`{invalid`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望 JSON 解析错误")
	}
}

func TestLoadEmptyRegions(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": []}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望空区域配置错误")
	}
}

func TestLoadDefaultConfig(t *testing.T) {
	path := filepath.Join("..", "..", "config", "collectible-regions.json")
	regions, err := LoadCollectibleRegions(path)
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}
	if len(regions) != 20 {
		t.Fatalf("默认区域数 = %d, want 20", len(regions))
	}
	for i, region := range regions {
		if region.TargetCount != 5 {
			t.Fatalf("region[%d] TargetCount = %d, want 5", i, region.TargetCount)
		}
	}
}

func TestLoadEmptyID(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r2", "center": {"lat": 31.24, "lng": 121.49}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.51}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望空 ID 错误")
	}
}

func TestLoadDuplicateID(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r1", "center": {"lat": 31.24, "lng": 121.49}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.51}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望重复 ID 错误")
	}
}

func TestLoadInvalidCoordinates(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"纬度>90", `{"regions": [
			{"id": "r1", "center": {"lat": 91, "lng": 121.47}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
			{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
			{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
		]}`},
		{"经度>180", `{"regions": [
			{"id": "r1", "center": {"lat": 31.23, "lng": 181}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
			{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
			{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
		]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, []byte(tt.json))
			_, err := LoadCollectibleRegions(path)
			if err == nil {
				t.Fatal("期望无效坐标错误")
			}
		})
	}
}

func TestLoadInvalidRadius(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 0, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望无效半径错误")
	}
}

func TestLoadInvalidTargetCount(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 200, "targetCount": 0, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望无效 targetCount 错误")
	}
}

func TestLoadRespawnMinGreaterThanMax(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 20, "respawnMaxSeconds": 5},
		{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望 min > max 重生时间错误")
	}
}

func TestLoadZeroRespawnSeconds(t *testing.T) {
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.23, "lng": 121.47}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 0, "respawnMaxSeconds": 15},
		{"id": "r2", "center": {"lat": 31.24, "lng": 121.50}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.25, "lng": 121.52}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望零重生时间错误")
	}
}

func TestLoadOverlappingRegions(t *testing.T) {
	// r1 和 r2 中心相距 100m，半径各 200m，必然重叠
	path := writeTempConfig(t, []byte(`{"regions": [
		{"id": "r1", "center": {"lat": 31.2304, "lng": 121.4737}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r2", "center": {"lat": 31.2308, "lng": 121.4745}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15},
		{"id": "r3", "center": {"lat": 31.2350, "lng": 121.4790}, "radiusMeters": 200, "targetCount": 20, "respawnMinSeconds": 5, "respawnMaxSeconds": 15}
	]}`))
	_, err := LoadCollectibleRegions(path)
	if err == nil {
		t.Fatal("期望重叠区域错误")
	}
}
