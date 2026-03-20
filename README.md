# Mimic Protocol

<div align="center">
  <img src="assets/logo.png" alt="Mimic Protocol Logo" width="200"/>

**[🇷🇺 Русский](README.md) | [🇺🇸 English](README.en.md)**

**Mimic** — это открытый протокол обхода блокировок, который постоянно меняет своё "цифровое лицо", имитируя трафик различных легитимных сервисов (VK, Rutube, Telegram и др.).

[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25.5-00ADD8.svg)](https://golang.org)

</div>

---

## 🎯 Суть проекта
Вместо того чтобы просто шифровать трафик (что часто выделяется для систем DPI), Mimic маскирует его под обычную активность пользователя.
1. **Полиморфизм:** Протокол динамически переключает профили поведения.
2. **Мимикрия:** Трафик выглядит как просмотр видео, общение в мессенджере или скроллинг соцсети.
3. **Неуловимость:** Нет постоянной сигнатуры, которую можно заблокировать.

## 🛠️ Самописные технологии

### MTP — Mimic Transport Protocol

**MTP** — полностью самописный транспортный протокол поверх UDP, разработанный с нуля как замена TCP для обхода DPI.

| Компонент | Описание |
|-----------|----------|
| **QUIC маскировка** | Пакеты на 100% маскируются под HTTP/3 (QUIC Short Header). Сервер защищен Active Probing Defender (отвечает фейковыми DNS пакетами сканерам) |
| **Полиморфные заголовки** | Структура каждого пакета уникальна. Умный Padding динамически растягивает размер до MTU, имитируя видео-поток. DPI не может написать регулярку |
| **ChaCha20-Poly1305** | Каждый пакет шифруется индивидуально. Перешифровка при ретрансмиссии (новый nonce) |
| **ARQ Engine** | Гарантия доставки: скользящее окно, Selective ACK, адаптивный RTO (Jacobson/Karels) |
| **BBR Congestion Control** | Контроль перегрузок на базе замера пропускной способности (BtlBw) и пинга (Min RTT). Полностью заменяет устаревший алгоритм AIMD, давая макс. скорость |
| **Forward Error Correction**| Модуль (Reed-Solomon), который восстанавливает потерянные UDP-пакеты прямо на лету без ожидания ретрансмиссии (идеально для плохих 4G сетей) |
| **Session Migration** | Бесшовная ротация: клиент мигрирует сессию на новый UDP-сокет без потери данных |
| **Keepalive** | Автоматический PING/PONG каждые 5 секунд, обнаружение мёртвых соединений |

#### Как это работает

```
Клиент                                    Сервер
  │                                          │
  │──── SYN (AUTH:uuid, зашифр.) ──────────>│
  │<─── SYN-ACK (OK, зашифр.) ─────────────│
  │                                          │
  │──── DATA [junk][nonce][encrypted] ─────>│  (каждый пакет выглядит иначе)
  │<─── ACK + SACK ────────────────────────│
  │                                          │
  │   === Ротация (бесшовная) ===           │
  │──── SYN+MIGRATE (session_id) ─────────>│  (новый UDP-сокет)
  │<─── SYN-ACK ───────────────────────────│  (сервер меняет адрес)
  │                                          │  (yamux не замечает)
```

### Полиморфный пакет MTP

```
[ QUIC Header: 9 байт ][ Padding: до 1350 байт ][ Nonce: 24 байт ][ Encrypted(Header+Payload) ]
   ↑ Фейковый префикс       ↑ Smart Padding         ↑ уникален               ↑ ChaCha20-Poly1305
       для обхода DPI        (маскировка размера)     для пакета
```

**Ни один DPI не может перехватить этот трафик**, потому что:
- Каждый пакет имеет разный размер (junk padding)
- Нет фиксированных маркеров или магических байтов
- Даже ретрансмиссия того же пакета выглядит полностью иначе (новый nonce + новый padding)

## 🏗️ Структура репозитория

```
Mimic-Protocol/
├── cmd/                # Исполняемые файлы
│   ├── client/         # CLI клиент с SOCKS5 прокси
│   └── server/         # Серверная часть (MTP)
├── pkg/                # Публичные библиотеки
│   ├── mtp/            # ★ MTP — самописный транспорт поверх UDP
│   ├── protocol/       # Ядро протокола (TLS-mimicry, legacy)
│   ├── transport/      # VirtualConn + Manager (бесшовная ротация)
│   ├── proxy/          # SOCKS5 прокси-сервер
│   ├── mimic/          # Генератор трафик-паттернов
│   ├── presets/        # Пресеты поведения (social, video, messenger)
│   └── config/         # Конфигурация с валидацией
├── internal/           # Внутренние компоненты
└── docs/               # Документация
```

## 🔧 Как это работает

### Базовый принцип
1. Пользователь задает список "белых" доменов (например, `vk.com`, `rutube.ru`).
2. Mimic устанавливает **MTP-соединение** (UDP) с сервером.
3. Поверх MTP работает **yamux** для мультиплексирования потоков.
4. Клиент поднимает **SOCKS5 прокси** (`127.0.0.1:1080`) с полной поддержкой **UDP Associate** (онлайн игры, DNS, WebRTC работают через туннель).
5. **Встроенный Routing Engine** гибко направляет трафик (`direct`, `proxy`, `block`) по правилам.
6. Каждые 30-600 секунд происходит **бесшовная ротация** транспорта.

## 📋 Конфигурация

> ⚠️ **Важно:** Библиотека `goccy/go-yaml` не поддерживает комментарии в конфигурационных файлах. При редактировании конфигов удаляйте комментарии (строки начинающиеся с `#`).

### Настройка сервера (`server.yaml`)

Создайте файл конфигурации на основе примера:

```bash
cp config.example.yaml server.yaml
nano server.yaml  # отредактируйте под себя (удалите комментарии!)
```

#### Все настройки сервера

| Параметр | Тип | Обязательный | Описание | Пример |
|----------|-----|--------------|----------|--------|
| `port` | int | ❌ | Порт прослушивания MTP (UDP). По умолчанию: `443` | `443`, `8443`, `8080` |
| `uuid` | string | ✅ | Уникальный UUID для аутентификации клиентов | `"550e8400-e29b-41d4-a716-446655440000"` |
| `name` | string | ❌ | Название сервера (отображается в логах и ссылках) | `"My-Mimic-Server"` |
| `transport` | string | ❌ | Тип транспорта: `"mtp"` (UDP, рекомендуется) или `"tcp"` (устаревший) | `"mtp"` |
| `domain_list` | []object | ❌ | Список доменов для мимикрии трафика (с опциональным пресетом) | `[{"domain": "vk.com", "preset": "social"}]` |
| `max_clients` | int | ❌ | Максимум одновременных клиентов. `0` = без ограничений | `100` |
| `dns` | string | ❌ | DNS-сервер для резолвинга доменов | `"1.1.1.1:53"` |
| `compression.enable` | bool | ❌ | Включить сжатие zstd. По умолчанию: `false` | `true`, `false` |
| `compression.level` | int | ❌ | Уровень сжатия (1-3): 1=Fastest, 2=Default, 3=Better | `2` |
| `compression.min_size` | int | ❌ | Мин. размер для сжатия (байты). По умолчанию: `64` | `64`, `128` |

#### Пример минимального конфига сервера

```yaml
port: 443
uuid: "550e8400-e29b-41d4-a716-446655440000"
name: "My-Mimic-Server"
transport: "mtp"

# Домены для мимикрии (с опциональным пресетом)
# Формат: domain (автоопределение) или domain:preset (явное указание)
domain_list:
  # Автоопределение пресета по домену
  - vk.com                    # Авто: social
  - rutube.ru                 # Авто: video
  - telegram.org              # Авто: messenger
  
  # Явное указание пресета для конкретного домена
  - domain: "some-gaming-site.com"
    preset: "gaming"          # Игровой трафик для этого домена
  
  - domain: "my-video-site.com"
    preset: "video"           # Видео стриминг для этого домена

max_clients: 100
dns: "1.1.1.1:53"

# Сжатие данных (опционально, выключено по умолчанию)
compression:
  enable: false  # true = включить сжатие zstd
  level: 2       # 1=Fastest, 2=Default, 3=Better
  min_size: 64   # Не сжимать пакеты < 64 байт
```

**Генерация UUID:**
```bash
./server generate-uuid
```

**Генерация ссылки для клиента:**
```bash
./server generate-link server.yaml
```

Пример вывода:
```
================================================================
🚀 Share this link with clients to connect:
================================================================
mimic://550e8400-e29b-41d4-a716-446655440000@your-server.com:443?name=My-Mimic-Server&domains=vk.com,rutube.ru&transport=mtp&dns=1.1.1.1:53
================================================================
```

---

### Настройка клиента (`client.yaml`)

#### Все настройки клиента

| Параметр | Тип | Обязательный | Описание | Пример |
|----------|-----|--------------|----------|--------|
| `server` | string | ✅ | Адрес сервера (IP:PORT или домен:PORT) | `"192.168.1.100:443"` |
| `uuid` | string | ✅ | UUID для аутентификации (должен совпадать с сервером) | `"550e8400-e29b-41d4-a716-446655440000"` |
| `domains` | []object | ❌ | Список доменов для мимикрии (с опциональным пресетом) | `[{"domain": "vk.com", "preset": "social"}]` |
| `transport` | string | ❌ | Тип транспорта: `"mtp"` или `"tcp"` | `"mtp"` |
| `local_port` | int | ❌ | Порт локального SOCKS5 прокси. По умолчанию: `1080` | `1080` |
| `dns` | string | ❌ | DNS-сервер для резолвинга | `"1.1.1.1:53"` |
| `compression.enable` | bool | ❌ | Включить сжатие zstd. По умолчанию: `false` | `true`, `false` |
| `compression.level` | int | ❌ | Уровень сжатия (1-3). По умолчанию: `2` | `1`, `2`, `3` |
| `compression.min_size` | int | ❌ | Мин. размер для сжатия. По умолчанию: `64` | `64`, `128` |
| `custom_presets` | map | ❌ | Кастомные пресеты для доменов (см. ниже) | `{...}` |
| `proxies` | []object | ❌ | Список локальных прокси (см. ниже) | `[{"type": "socks5", "port": 1080}]` |
| `routing.default_policy` | string | ❌ | Политика по умолчанию: `proxy`, `direct`, `block` | `"proxy"` |
| `routing.rules` | []object | ❌ | Правила маршрутизации (см. ниже) | `[...]` |
| `settings.switch_time` | string | ❌ | Интервал смены профиля (формат: `"60s-300s"` или `"1m-5m"`) | `"60s-300s"` |
| `settings.randomize` | bool | ❌ | Случайный порядок смены доменов | `true` |

#### Настройка прокси (`proxies`)

Клиент может поднимать несколько локальных прокси одновременно.

| Параметр | Тип | Описание |
|----------|-----|----------|
| `type` | string | Тип прокси: `"socks5"` (с поддержкой UDP) или `"http"` |
| `port` | int | Порт для прослушивания |

**Пример настройки прокси:**

```yaml
proxies:
  - type: "socks5"
    port: 1080
  - type: "http"
    port: 8080
```

#### Кастомные пресеты (`custom_presets`)

Система пресетов позволяет задать специфичное поведение трафика для разных сервисов.

**Встроенные пресеты:**
- `web_generic` — веб-серфинг (500-1420 байт, 10-150 PPS)
- `social` — соцсети (VK, Instagram)
- `video` — видеостриминг (YouTube, Twitch)
- `messenger` — мессенджеры (Telegram, WhatsApp)
- `gaming` — игры (CS2, Dota 2) — малые пакеты, высокий PPS
- `voip` — VoIP/видеозвонки (Discord, Zoom) — симметричный трафик

**Пример custom_presets:**

```yaml
custom_presets:
  # Gaming preset для Steam
  steampowered.com:
    name: "Gaming - Steam"
    type: "gaming"
    packet_size_min: 64
    packet_size_max: 512
    packets_per_sec_min: 30
    packets_per_sec_max: 120
    upload_download_ratio: 0.8
    session_duration: "600s-3600s"
  
  # VoIP preset для Discord
  discord.com:
    name: "VoIP - Discord"
    type: "voip"
    packet_size_min: 80
    packet_size_max: 300
    packets_per_sec_min: 20
    packets_per_sec_max: 50
    upload_download_ratio: 1.0
    session_duration: "300s-7200s"
  
  # Video preset для YouTube
  youtube.com:
    name: "Video - YouTube"
    type: "video"
    packet_size_min: 1000
    packet_size_max: 1450
    packets_per_sec_min: 50
    packets_per_sec_max: 200
    upload_download_ratio: 0.05
    session_duration: "300s-3600s"
```

**Приоритет выбора пресетов:**
1. Custom presets (точное совпадение домена)
2. Custom presets (по ключевому слову)
3. Default presets (по маппингу доменов)
4. `web_generic` (по умолчанию)

#### Настройка маршрутизации (`routing`)

Встроенный Routing Engine направляет трафик на основе правил.

**Политики:**
- `proxy` — направлять через туннель Mimic
- `direct` — подключаться напрямую (в обход туннеля)
- `block` — блокировать соединение

**Типы правил:**
- `domain_suffix` — совпадение по суффиксу домена (например, `ru`, `org`)
- `domain_keyword` — совпадение по ключевому слову в домене
- `ip_cidr` — совпадение по IP-диапазону (CIDR-нотация)

**Пример настройки маршрутизации:**

```yaml
routing:
  default_policy: proxy
  rules:
    - type: domain_suffix
      value: ru
      policy: direct
    - type: domain_suffix
      value: cn
      policy: block
    - type: ip_cidr
      value: 192.168.0.0/16
      policy: direct
    - type: domain_keyword
      value: google
      policy: proxy
```

#### Пример полного конфига клиента

```yaml
server: "your-mimic-server.com:443"
uuid: "550e8400-e29b-41d4-a716-446655440000"
local_port: 1080

# Домены для мимикрии (с опциональным пресетом)
# Формат: domain (автоопределение) или domain:preset (явное указание)
domains:
  # Автоопределение пресета по домену
  - vk.com                    # Авто: social
  - rutube.ru                 # Авто: video
  - telegram.org              # Авто: messenger
  
  # Явное указание пресета для конкретного домена
  - domain: "some-gaming-site.com"
    preset: "gaming"          # Игровой трафик для этого домена
  
  - domain: "my-video-site.com"
    preset: "video"           # Видео стриминг для этого домена

transport: "mtp"
dns: "1.1.1.1:53"

# Сжатие данных (опционально)
compression:
  enable: false  # true = включить сжатие zstd
  level: 2       # 1=Fastest, 2=Default, 3=Better
  min_size: 64   # Не сжимать пакеты < 64 байт

# Кастомные пресеты
custom_presets:
  discord.com:
    type: "voip"
    packet_size_min: 80
    packet_size_max: 300
    packets_per_sec_min: 20
    packets_per_sec_max: 50

proxies:
  - type: "socks5"
    port: 1080
  - type: "http"
    port: 8080

routing:
  default_policy: proxy
  rules:
    - type: domain_suffix
      value: ru
      policy: direct
    - type: ip_cidr
      value: 127.0.0.0/8
      policy: block

settings:
  switch_time: "60s-300s"
  randomize: true
```


## 📦 Используемые библиотеки (Go)

| Библиотека | Назначение |
|------------|------------|
| **[github.com/goccy/go-yaml](https://github.com/goccy/go-yaml)** | Быстрый YAML-парсер (в 10 раз быстрее стандартного) |
| **[github.com/hashicorp/yamux](https://github.com/hashicorp/yamux)** | Мультиплексирование потоков поверх MTP |
| **[github.com/klauspost/reedsolomon](https://github.com/klauspost/reedsolomon)** | Сверхбыстрая реализация FEC для восстановления потерь |
| **[github.com/refraction-networking/utls](https://github.com/refraction-networking/utls)** | Подмена TLS Fingerprint (имитация реальных браузеров) |
| **[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305)** | Надежное шифрование ChaCha20-Poly1305 |
| **[github.com/google/uuid](https://github.com/google/uuid)** | Генерация и парсинг UUID для авторизации |
| **[github.com/klauspost/compress](https://github.com/klauspost/compress)** | Высокопроизводительное сжатие данных |

## 🚀 Использование

### ⚡ Быстрая установка на Linux (автоматическая)

**Требования:** Ubuntu/Debian, CentOS/RHEL/Fedora, Arch Linux (root-доступ)

```bash
# 1. Клонирование репозитория
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol

# 2. Запуск установщика (от root)
sudo bash scripts/linux/install.sh

# 3. Готово! Сервер установлен и запущен
```

Установщик автоматически:
- ✅ Скачает готовый бинарник для вашей архитектуры (amd64/arm64)
- ✅ Установит зависимости (Go, systemd, jq)
- ✅ Сгенерирует UUID и создаст конфиг в `/etc/mimic/server.yaml`
- ✅ Настроит systemd-сервис
- ✅ Включит автозапуск при загрузке

#### Управление сервером через CLI

После установки доступна команда `mimic`:

```bash
mimic status-server      # Статус сервера
mimic restart-server     # Перезапуск
mimic stop-server        # Остановка
mimic generate-uuid      # Генерация UUID
mimic generate-link      # Ссылка для клиента
mimic config_path        # Путь к конфигу
```

#### Ручная настройка фаервола

```bash
# UFW (Ubuntu/Debian)
sudo ufw allow 443/udp
sudo ufw reload

# firewalld (CentOS/Fedora)
sudo firewall-cmd --permanent --add-port=443/udp
sudo firewall-cmd --reload
```

---

### 🔧 Ручная установка на Linux

Если вы предпочитаете ручную настройку или хотите собрать из исходников:

```bash
# 1. Установка Go
sudo apt update && sudo apt install -y golang-go  # Ubuntu/Debian
# или
sudo dnf install -y golang  # CentOS/Fedora

# 2. Сборка
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol
go build -o server ./cmd/server
chmod +x server

# 3. Настройка
cp config.example.yaml server.yaml  # или создайте свой конфиг
./server generate-uuid              # сгенерируйте UUID и вставьте в конфиг

# 4. Запуск
./server -config server.yaml
```

---

### 🌐 Кросс-компиляция

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -o server.exe ./cmd/server

# macOS ARM64
GOOS=darwin GOARCH=arm64 go build -o server ./cmd/client
```

---

### 📱 Запуск клиента

```bash
./client -config config.yaml
```

При успешном подключении клиент выведет:
```
✅ Session established!
🌐 SOCKS5 Proxy: 127.0.0.1:1080
  ↑ 125.3 KB/s  ↓ 1.2 MB/s  │  Traffic: 45.6 MB  │  Connected: 00:15:32  │  Active: 3
```

---

### 📲 Официальное приложение (Beta)

Для удобного использования доступны готовые приложения с графическим интерфейсом:

**[Mimic App](https://github.com/Locon213/Mimic-App)** — официальное клиентское приложение (в бете)

Доступные платформы:
- 🐧 **Linux** 
- 🍎 **macOS** 
- 🪟 **Windows** 
- 🤖 **Android** 

Приложение включает:
- ✅ Графический интерфейс управления
- ✅ Импорт конфигураций по ссылке (`mimic://...`)
- ✅ Статистика трафика в реальном времени

---

### 🔌 Использование как Go SDK

Вы можете встроить клиент Mimic Protocol в собственное Go-приложение (например, GUI или мобильное приложение). Подробнее см. **[Документацию по SDK](docs/sdk.md)**.

## 🔐 Безопасность
- **Транспорт:** MTP (самописный протокол поверх UDP) с ChaCha20-Poly1305 шифрованием
- **Полиморфизм:** Каждый пакет уникален — DPI не может создать сигнатуру
- **Обмен ключами:** Авторизация по UUID
- **Анонимность:** Сервер не хранит логи, регистрация не требуется

## 📄 Лицензия
Проект распространяется под лицензией GPLv3. Подробнее см. файл [LICENSE](LICENSE).

Copyright (c) 2025-н.в. Locon213 & Contributors.
