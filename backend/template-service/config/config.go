package config

import (
	"io/ioutil"
	"template-service/pkg/config"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// LoadConfig загружает конфигурацию из файла dev.yaml или config.yaml.
func LoadConfig() *config.Config {
	cfg := &config.Config{}

	// Чтение YAML-конфигурационного файла
	data, err := ioutil.ReadFile("./config/dev.yaml")
	if err != nil {
		data, err = ioutil.ReadFile("./config/config.yaml")
		if err != nil {
			// Временно использовать zap.NewExample() для logger.Fatal, если нет DI логгера
			zapLogger := zap.NewExample()
			zapLogger.Fatal("Ошибка чтения файла config.yaml", zap.Error(err))
		}
	}

	// Парсинг YAML-данных в структуру Config
	err = yaml.Unmarshal(data, cfg)
	if err != nil {
		// Временно использовать zap.NewExample() для logger.Fatal, если нет DI логгера
		zapLogger := zap.NewExample()
		zapLogger.Fatal("Ошибка парсинга файла config.yaml", zap.Error(err))
	}

	return cfg
}
