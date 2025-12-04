package loop

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"template-service/internal/usecase"
	"template-service/pkg/config"

	"github.com/google/uuid"
)

const (
	DemoStream = "template-demo-stream"
)

type DemoEvent struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	TraceID   string `json:"trace_id"`
	Timestamp int64  `json:"timestamp"`
}

// StartEventLoop запускает цепочку: HTTP -> DB/MinIO -> NATS -> gRPC -> Redis -> HTTP
func StartEventLoop(uc *usecase.DemoUseCase, natsCfg config.NatsConfig) {
	// Гарантируем наличие стрима JetStream (если требуется)
	if err := uc.EnsureStream(natsCfg.DemoSubject, DemoStream); err != nil {
		fmt.Println("EnsureStream error:", err)
	}

	go func() {
		for {
			time.Sleep(10 * time.Second)
			// 1. Сгенерировать данные для демо
			userID := uuid.New().String()
			value := uuid.New().String()
			region := "eu"

			// 2. Отправить POST /demo с id, value, region_id
			jsonBody := fmt.Sprintf(`{"user_id":"%s","value":"%s","region_id":"%s"}`, userID, value, region)
			respPost, err := http.Post("http://localhost:8080/demo", "application/json", strings.NewReader(jsonBody))
			if err == nil {
				defer respPost.Body.Close()
				body, _ := ioutil.ReadAll(respPost.Body)
				fmt.Println("[LOOP] POST /demo response:", string(body))
			} else {
				fmt.Println("[LOOP] POST /demo error:", err)
			}

			// 3. Дальнейшие действия происходят по подписке на NATS (SubscribeEvent ниже)
			// (Создание файла, upload, запись в БД и публикация события теперь реализуются в обработчике POST /demo)
		}
	}()

	// Пример подписки на DemoSubject
	err := uc.SubscribeEvent(natsCfg.DemoSubject, func(data []byte, headers map[string]string) error {
		var event map[string]interface{}
		err := json.Unmarshal(data, &event)
		if err != nil {
			fmt.Println("NATS event unmarshal error:", err)
			return err
		}
		fmt.Println("[NATS] Получено событие:", event)
		// Безопасно извлекаем id (см. предыдущую логику)
		idRaw, ok := event["ID"]
		if !ok || idRaw == nil {
			idRaw, ok = event["id"] // lowercase fallback
			if !ok || idRaw == nil {
				fmt.Println("NATS event: поле ID отсутствует или nil")
				return nil
			}
		}
		var id string
		switch v := idRaw.(type) {
		case string:
			id = v
		default:
			fmt.Println("NATS event: поле ID не string и не число, а:", v)
			return nil
		}
		if id == "" {
			if action, ok := event["Action"].(string); ok && action == "created" {
				if traceID, ok := event["TraceID"].(string); ok && traceID != "" {
					id = traceID
				} else {
					id = uuid.New().String()
				}
				fmt.Println("NATS event: подставлен fallback ID:", id)
			} else {
				fmt.Println("NATS event: поле ID пустое")
				return nil
			}
		}

		// 4. После получения события — отправить GET /demo/:id
		resp, err := http.Get(fmt.Sprintf("http://localhost:8080/demo/%s", id))
		if err == nil {
			defer resp.Body.Close()
			body, _ := ioutil.ReadAll(resp.Body)
			fmt.Println("[NATS] GET /demo/:id response:", string(body))
		} else {
			fmt.Println("HTTP GET /demo/:id error:", err)
		}
		return nil
	})
	if err != nil {
		fmt.Println("SubscribeEvent error:", err)
	}

	// Пример подписки на DLQ
	err = uc.SubscribeEvent(natsCfg.DemoDLQSubject, func(data []byte, headers map[string]string) error {
		var event map[string]interface{}
		if err := json.Unmarshal(data, &event); err != nil {
			fmt.Println("NATS DLQ unmarshal error:", err)
			return err
		}
		fmt.Println("[NATS DLQ] Получено событие:", event)
		return nil
	})
	if err != nil {
		fmt.Println("NATS DLQ subscribe error:", err)
	}
}
