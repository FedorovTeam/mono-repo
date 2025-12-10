## 1️⃣ Полный админ (consoleAdmin)

Чаще всего можно просто использовать встроенный `consoleAdmin`, но если хочешь свой:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:*"],
      "Resource": ["arn:aws:s3:::*"]
    },
    {
      "Effect": "Allow",
      "Action": ["admin:*"],
      "Resource": ["arn:aws:s3:::*"]
    }
  ]
}

```

Создать:

```bash
mc admin policy create myminio consoleAdmin consoleAdmin.json

```

---

## 2️⃣ Чтение/запись по одному бакету (readwrite)

Например, дать пользователю полный доступ только к бакету `my-bucket`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation",
        "s3:ListBucket",
        "s3:ListBucketMultipartUploads"
      ],
      "Resource": ["arn:aws:s3:::my-bucket"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": ["arn:aws:s3:::my-bucket/*"]
    }
  ]
}

```

Создать:

```bash
mc admin policy create myminio readwrite readwrite.json

```

Если нужно на **все бакеты**, поменяй `my-bucket` → `*` и `my-bucket/*` → `*`.

---

## 3️⃣ Только чтение бакета (readonly)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation",
        "s3:ListBucket"
      ],
      "Resource": ["arn:aws:s3:::my-bucket"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject"
      ],
      "Resource": ["arn:aws:s3:::my-bucket/*"]
    }
  ]
}

```

Добавить в authentik groups - нужную policy, пример consoleAdmin

В Identity -> OpenID указать
Callback URL: 
```shell
https://console.minio.tech.fedorov-team.tech/oauth_callback
```