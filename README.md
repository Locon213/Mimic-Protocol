# Mimic Protocol

<div align="center">

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

### Конфигурация
Пример `config.yaml` для клиента:

```yaml
server: "your-mimic-server.com:443"
uuid: "your-uuid-here"
local_port: 1080  # Порт SOCKS5 прокси с поддержкой TCP/UDP

# Движок маршрутизации (Опционально)
routing:
  default_policy: proxy
  rules:
    - type: domain_suffix
      value: ru
      policy: direct
    - type: ip_cidr
      value: 127.0.0.0/8
      policy: block

domains:
  - vk.com          # Пресет "social"
  - rutube.ru       # Пресет "video"
  - telegram.org    # Пресет "messenger"

settings:
  switch_time: "60s-300s"   # Менять профиль каждые 1-5 минут
  randomize: true           # Случайный порядок смены доменов
```

## 📦 Используемые библиотеки (Go)
В проекте используются следующие мощные Open-Source решения:
- **[hashicorp/yamux](https://github.com/hashicorp/yamux)** — мультиплексирование потоков поверх MTP.
- **[klauspost/reedsolomon](https://github.com/klauspost/reedsolomon)** — сверхбыстрая реализация FEC для восстановления потерь.
- **[refraction-networking/utls](https://github.com/refraction-networking/utls)** — подмена TLS Fingerprint (имитация реальных браузеров).
- **[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305)** — надежное шифрование ChaCha20-Poly1305.
- **[google/uuid](https://github.com/google/uuid)** — генерация и парсинг UUID для авторизации.
- **[go-yaml/yaml](https://github.com/go-yaml/yaml)** — парсинг конфигурационных файлов.

## 🚀 Использование

### Сборка из исходников

```bash
# Клонирование
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol

# Сборка сервера и клиента
go build -o server ./cmd/server
go build -o client ./cmd/client
```

### Генерация UUID

```bash
./server generate-uuid
# Выведет: 550e8400-e29b-41d4-a716-446655440000
```

### Запуск сервера

```bash
./server -config server.yaml
```

### Запуск клиента

```bash
./client -config config.yaml
```

При успешном подключении клиент выведет:
```
✅ Session established!
🌐 SOCKS5 Proxy: 127.0.0.1:1080
  ↑ 125.3 KB/s  ↓ 1.2 MB/s  │  Traffic: 45.6 MB  │  Connected: 00:15:32  │  Active: 3
```

## 🔐 Безопасность
- **Транспорт:** MTP (самописный протокол поверх UDP) с ChaCha20-Poly1305 шифрованием
- **Полиморфизм:** Каждый пакет уникален — DPI не может создать сигнатуру
- **Обмен ключами:** Авторизация по UUID
- **Анонимность:** Сервер не хранит логи, регистрация не требуется

## 📄 Лицензия
Проект распространяется под лицензией GPLv3. Подробнее см. файл [LICENSE](LICENSE).

Copyright (c) 2025-н.в. Locon213 & Contributors.
