# 📘 Инструкция: как получить JSON-конфиг для Outline Manager

## Что в итоге должно получиться

Ты вставляешь в Outline Manager **ровно такой JSON**:

```json
{
"apiUrl":"https://vpn.tech.fedorov-team.tech/cjaorBbzegUKLvKxJhqzug",
"certSha256":"F1CDDB3615A4E388DEC1EFF62D10DA0E4A46000B3790DF870656698AEEA67D7B"
}

```

Ниже — **как получить каждое поле шаг за шагом**.

---

## 1️⃣ Получить `apiUrl`

### 1.1 Узнай `SB_API_PREFIX`

```bash
kubectl -n outline-vpn get deploy shadowbox \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SB_API_PREFIX")].value}'

```

Пример вывода:

```
cjaorBbzegUKLvKxJhqzug

```

### 1.2 Сформируй `apiUrl`

Формула:

```
https://<DOMAIN>/<SB_API_PREFIX>

```

В твоём случае:

```
https://vpn.tech.fedorov-team.tech/cjaorBbzegUKLvKxJhqzug

```

---

## 2️⃣ Получить `certSha256`

> Fingerprint берётся из TLS-сертификата Management API,
>
>
> а не из VPN-ключей.
>

### 2.1 Извлеки сертификат из Kubernetes Secret

```bash
kubectl -n outline-vpn get secret shadowbox-tls \
  -o jsonpath='{.data.tls\.crt}' |base64 -d > /tmp/shadowbox.crt

```

### 2.2 Посчитай SHA-256 fingerprint

```bash
openssl x509 -in /tmp/shadowbox.crt -noout -fingerprint -sha256

```

Пример вывода:

```
SHA256Fingerprint=F1:CD:DB:36:15:A4:E3:88:DE:C1:EF:F6:2D:10:DA:0E:4A:46:00:0B:37:90:DF:87:06:56:69:8A:EE:A6:7D:7B

```

### 2.3 Приведи fingerprint к нужному формату

Outline Manager требует:

- **без `SHA256 Fingerprint=`**
- **без `:`**

```bash
openssl x509 -in /tmp/shadowbox.crt -noout -fingerprint -sha256 \
  | sed's/^.*=//; s/://g'

```

Результат:

```
F1CDDB3615A4E388DEC1EFF62D10DA0E4A46000B3790DF870656698AEEA67D7B

```

---

## 3️⃣ Собери финальный JSON

```json
{
"apiUrl":"https://vpn.tech.fedorov-team.tech/cjaorBbzegUKLvKxJhqzug",
"certSha256":"F1CDDB3615A4E388DEC1EFF62D10DA0E4A46000B3790DF870656698AEEA67D7B"
}

```

---

## 4️⃣ Проверка перед вставкой в Outline Manager (обязательно)

### 4.1 API реально доступен?

```bash
curl -sk https://vpn.tech.fedorov-team.tech/cjaorBbzegUKLvKxJhqzug/access-keys

```

Ожидаемый ответ:

```json
{"accessKeys":[]}

```

### 4.2 Если тут не `200` — Manager не подключится

---

## 5️⃣ Вставка в Outline Manager

1. Открой **Outline Manager**
2. Нажми **Add server**
3. Выбери **Paste configuration**
4. Вставь JSON целиком
5. Подключись

---

##