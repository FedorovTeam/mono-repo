package domain

import "github.com/google/uuid"

// GenerateBucketID генерирует bucket_id по правилам архитектуры MoiQR
// Для B2C: bucket_id = user_id (UUID)
// Для B2B: bucket_id = shop_id (UUID)
// Если не передан user/shop id — генерирует новый UUID (DEMO ONLY)
func GenerateBucketID(userOrShopID string) string {
	if userOrShopID != "" {
		return userOrShopID
	}
	return uuid.New().String()
}

// GetRegionID возвращает region_id из переменной окружения SERVICE_REGION
// Если не задано — возвращает "default-region"
var regionID string

// SetRegionID сохраняет значение region_id из конфигурации.
func SetRegionID(r string) {
	regionID = r
}

// GetRegionID возвращает region_id из конфигурации
// Если не задано — возвращает "default-region".
func GetRegionID() string {
	if regionID == "" {
		return "default-region"
	}
	return regionID
}
