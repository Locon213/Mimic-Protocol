# Продвинутое руководство по Mimic Client SDK

**Mimic Client SDK** — это набор инструментов для разработчиков, позволяющий встраивать функциональность протокола Mimic (MTP) непосредственно в ваши собственные приложения. Будь то десктопное приложение с графическим интерфейсом (GUI), оболочка для Android (через gomobile/cgo), или специализированная утилита командной строки, SDK предоставляет вам полный программный контроль над соединением и локальными прокси-серверами.

## Установка

Подключите библиотеки Mimic Protocol в ваш Go-проект:

```bash
go get github.com/Locon213/Mimic-Protocol/pkg/client
go get github.com/Locon213/Mimic-Protocol/pkg/config
```

```

## URL-Схемы конфигурации (как в VLESS)

Mimic поддерживает удобную передачу конфигураций сервера через формат единой ссылки (`mimic://...`), аналогичный VLESS/VMess. Это позволяет быстро делиться ключами доступа. Вы также можете указывать имя сервера в конце (`#Name`).

### Генерация ссылки
На стороне сервера или через скрипты администратора:
```bash
mimic-server generate-link server.yaml
# или через PowerShell: ./mimic.ps1 generate-link
```
Результат: `mimic://<uuid>@<server_ip>:<port>?domains=vk.com,rutube.ru&transport=mtp#MyServer`

### Использование ссылки в Клиенте
Через SDK:
```go
cfg, err := config.ParseMimicURL("mimic://uuid@ip:port?domains=vk.com#MyServer")
// ...
mimicClient, _ := client.NewClient(cfg)
```
Через CLI: `mimic-client -url "mimic://...#MyServer"`

## Основные концепции

Разработка с Mimic SDK вращается вокруг объекта конфигурации (`config.ClientConfig`) и самого экземпляра клиента (`client.Client`).

Жизненный цикл клиента состоит из следующих этапов:
1. **Подготовка конфигурации**: определение пути до сервера, вашего UUID, доменов для маскировки (SNI) и правил маршрутизации.
2. **Инициализация**: вызов `client.NewClient(cfg)` создает внутренние структуры (обработчики маршрутизации, менеджер соединений). Данный этап не совершает сетевых вызовов.
3. **Запуск соединения**: `client.Start(ctx)` устанавливает MTP соединение к удаленному серверу, выполняет handshake (рукопожатие) и запускает фоновую генерацию шумового (фиктивного) трафика.
4. **Запуск прокси**: `client.StartProxies()` биндит SOCKS5/HTTP порты (127.0.0.1:...) и начинает обрабатывать локальные подключения пользователей. Внутренний роутер перенаправляет трафик в туннель или напрямую.
5. **Остановка**: `client.Stop()` для изящного закрытия всех слушателей портов и завершения MTP-соединения.

## Структура конфигурации (`ClientConfig`)

Конфигурация — самая важная часть, определяющая поведение клиента. Ниже приведены основные параметры, которыми вы можете управлять программно.

```go
type ClientConfig struct {
    Server        string                        // Адрес сервера (IP:PORT)
    UUID          string                        // Уникальный идентификатор авторизации
    Domains       []DomainEntry                 // Список доменов для маскировки (с опциональным пресетом)
    Transport     string                        // Тип транспорта: "mtp" (рекомендуется) или "tcp"
    Proxies       []ProxyConfig                 // Настройки локальных серверов (SOCKS5, HTTP)
    DNS           string                        // Свой кэширующий DNS-сервер (например, "1.1.1.1:53")
    Settings      ClientSettings                // Тонкие настройки генератора трафика
    Routing       RoutingConfig                 // Правила маршрутизации
    Compression   CompressionConfig             // Настройки сжатия данных
    Android       AndroidConfig                 // Настройки для Android (TUN, VpnService)
    CustomPresets map[string]CustomPresetConfig // Пользовательские пресеты трафика
}

// DomainEntry представляет домен с опциональным пресетом
type DomainEntry struct {
    Domain string // Имя домена
    Preset string // Имя пресета (опционально, пусто = автоопределение)
}
```

### Доступные пресеты

Mimic SDK включает несколько встроенных пресетов для различных типов трафика:

| Пресет | Описание | Примеры доменов |
|--------|----------|-----------------|
| `web_generic` | Веб-серфинг (по умолчанию) | wikipedia.org, dzen.ru |
| `social` | Социальные сети | vk.com, instagram.com, facebook.com |
| `video` | Видео стриминг | youtube.com, twitch.tv, netflix.com |
| `messenger` | Мессенджеры | telegram.org, whatsapp.com, signal.org |
| `gaming` | Игры | steampowered.com, epicgames.com |
| `voip` | VoIP сервисы | discord.com, zoom.us, skype.com |

Вы можете указать пресет явно для конкретного домена:
```go
Domains: []config.DomainEntry{
    {Domain: "vk.com", Preset: "social"},           // Явное указание пресета
    {Domain: "youtube.com", Preset: "video"},        // Явное указание пресета
    {Domain: "wikipedia.org"},                        // Автоопределение пресета
    {Domain: "some-gaming-site.com", Preset: "gaming"}, // Игровой трафик
}
```

### Настройки маскировки (`ClientSettings`)

Для затруднения глубокого анализа трафика со стороны DPI, Mimic SDK автоматически может менять домены (SNI и профили поведения) прямо "на лету" без разрыва соединения.
```go
Settings: config.ClientSettings{
    SwitchTimeRangeStr: "60s-300s", // Каждые 1-5 минут домен маскировки случайным образом меняется
}
```

### Настройки локальных прокси (`Proxies`)

Вы можете запустить сразу несколько прокси-серверов внутри одного клиента, например SOCKS5 на порту 1080 и классический HTTP/HTTPS прокси на 1081.
```go
Proxies: []config.ProxyConfig{
    {Type: "socks5", Port: 1080},
    {Type: "http", Port: 1081},
},
```

### Встроенная маршрутизация (`RoutingConfig`)

SDK поддерживает продвинутый роутинг, позволяя обходить локальные ресурсы (Direct) или блокировать трекеры.
Возможные типы правил (`Type`): `domain_suffix` (суффикс домена), `domain_keyword` (ключевое слово в домене), `ip_cidr` (IP-адреса и подсети).
Политики (`Policy`): `proxy` (направлять в туннель), `direct` (идти напрямую в обход туннеля), `block` (заблокировать запрос).

```go
Routing: config.RoutingConfig{
    DefaultPolicy: "proxy", // По умолчанию весь трафик идет в туннель
    Rules: []config.RoutingRule{
        {Type: "domain_suffix", Value: "ru", Policy: "direct"},       // Не пускать RU сайты в туннель
        {Type: "ip_cidr", Value: "192.168.0.0/16", Policy: "direct"}, // Локальная сеть напрямую
        {Type: "domain_keyword", Value: "ads", Policy: "block"},      // Блокировка рекламы
    },
}
```

## Полный пример консольного клиента

Здесь представлен рабочий код CLI-клиента, который вы можете использовать как каркас.

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Locon213/Mimic-Protocol/pkg/client"
	"github.com/Locon213/Mimic-Protocol/pkg/config"
)

func main() {
	// 1. Формируем конфигурацию программно
	cfg := &config.ClientConfig{
		Server:    "12.34.56.78:443", // Замените на ваш сервер
		UUID:      "12345678-1234-1234-1234-1234567890ab",
		Domains: []config.DomainEntry{
			{Domain: "vk.com", Preset: "social"},      // Явное указание пресета
			{Domain: "yandex.ru"},                       // Автоопределение пресета
			{Domain: "some-gaming-site.com", Preset: "gaming"}, // Игровой трафик
		},
		Transport: "mtp",
		DNS:       "8.8.8.8:53",
		Settings: config.ClientSettings{
			SwitchTimeRangeStr: "120s-600s",
		},
		Proxies: []config.ProxyConfig{
			{Type: "socks5", Port: 1080},
			{Type: "http", Port: 1081},
		},
		Routing: config.RoutingConfig{
			DefaultPolicy: "proxy",
			Rules: []config.RoutingRule{
				{Type: "domain_suffix", Value: "ru", Policy: "direct"},
			},
		},
		Compression: config.CompressionConfig{
			Enable:  true,   // Включить сжатие
			Level:   2,      // Уровень сжатия (1-3)
			MinSize: 64,     // Минимальный размер для сжатия
		},
	}

	// 2. Инициализация SDK клиента
	mimicClient, err := client.NewClient(cfg)
	if err != nil {
		log.Fatalf("Ошибка инициализации SDK: %v", err)
	}

	// 3. Запуск MTP-соединения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mimicClient.Start(ctx); err != nil {
		log.Fatalf("Ошибка подключения к серверу: %v", err)
	}
	
	// 4. Запуск локальных SOCKS5 / HTTP
	if err := mimicClient.StartProxies(); err != nil {
		log.Fatalf("Ошибка запуска локальных обозревателей: %v", err)
	}
	
	log.Println("Mimic Protocol SDK успешно запущен и готов к работе!")

	// 5. Ожидание сигнала для изящного завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	<-sigChan
	log.Println("Получен сигнал завершения. Остановка клиента...")

	// 6. Graceful Shutdown
	mimicClient.Stop()
	log.Println("Клиент остановлен успешно.")
}
```

## Интеграция в GUI приложения (Мобильные, Desktop)

Если вы встраиваете Mimic в графическое приложение (например, написанное на **Flutter** (с помощью dart:ffi/gomobile), **Wails**, **Go-Qt**, **Fyne**, или нативное Android-приложение), вам необходимо запускать клиента в отдельной горутине, чтобы он не блокировал основной UI поток.

Рекомендуемый подход через сервис контроля жизненного цикла:

```go
type VpnService struct {
    client *client.Client
}

// StartService вызывается из вашего UI по кнопке "Connect"
func (v *VpnService) StartService(serverIp string, uuid string) error {
    cfg := &config.ClientConfig{
        Server: serverIp,
        UUID: uuid,
        Domains: []config.DomainEntry{
            {Domain: "example.com"}, // Автоопределение пресета
        },
        Transport: "mtp", // MTP использует UDP BBR
        DNS: "1.1.1.1:53", // Рекомендуется для обхода локальных DNS-утечек
        Proxies: []config.ProxyConfig{
			{Type: "socks5", Port: 1080},
			{Type: "http", Port: 1081}, // Удобно для системного прокси
		},
        Compression: config.CompressionConfig{
            Enable: true, // Включить сжатие для экономии трафика
        },
    }
    
    var err error
    v.client, err = client.NewClient(cfg)
    if err != nil {
        return err // Возвращаем ошибку в UI
    }
    
    // Блокирующий коннект к серверу. 
	// Лучше использовать context с таймаутом!
    if err := v.client.Start(context.Background()); err != nil {
        return err
    }
    
    // Биндим порты
    return v.client.StartProxies()
}

// StopService вызывается из вашего UI по кнопке "Disconnect"
func (v *VpnService) StopService() {
    if v.client != nil {
        v.client.Stop()
        v.client = nil
    }
}
```

При интеграции с пользовательским интерфейсом (UI), вы можете предоставлять пользователю поля для настроек в графическом виде (разрешать вводить DNS, порты или UUID), а под капотом собирать структуру `config.ClientConfig` и передавать её SDK.

### Использование статистик соединения (Скорость и Трафик)
Для вывода UI прогресса (скорость загрузки, статистика соединения), SDK поддерживает вывод данных о соединениях. Вы можете использовать `client.tm.GetMTPConn().BytesSent` для чтения текущего отправленного/полученного трафика внутри вашего кода!

## Дополнительные функции SDK

### `SendSpeedData(ctx context.Context, downloadSpeed, uploadSpeed int64, pingMs int64) error`
Отправляет данные о скорости загрузки, скачивания и пинге на сервер через MTP протокол. Это позволяет серверу мониторить качество соединения и принимать динамические решения о маршрутизации.

### `GetNetworkStats() NetworkStats`
Возвращает текущую статистику сети, включая:
- `DownloadSpeed` - скорость загрузки (байт/сек)
- `UploadSpeed` - скорость скачивания (байт/сек)
- `Ping` - пинг до сервера (мс)
- `TotalDownload` - всего загружено (байт)
- `TotalUpload` - всего скачано (байт)

### `GetConnectionStatus() ConnectionStatus`
Возвращает текущий статус подключения: `disconnected`, `connecting`, `connected`, `reconnecting`.

### `GetSessionInfo() *SessionInfo`
Возвращает информацию о текущей сессии:
- Время подключения
- Адрес сервера
- UUID
- Транспорт
- Текущий домен маскировки
- Время работы (uptime)

### `GetTrafficStats() (totalDownload, totalUpload int64)`
Возвращает общий объем трафика (скачано/загружено) за сессию.

### `GetCurrentDomain() string`
Возвращает текущий активный домен маскировки (SNI).

### `Reconnect(ctx context.Context) error`
Выполняет переподключение к серверу. Полезно при смене сети или потере соединения.

### `GetServerInfo() map[string]interface{}`
Возвращает информацию о конфигурации сервера (адрес, домены, UUID, транспорт, DNS).

### `SetTrafficCallback(callback TrafficCallback)`
Устанавливает callback-функцию, которая будет вызываться при обновлении статистики трафика. Позволяет UI приложениям получать实时ные данные о скорости.

### `IsConnected() bool`
Возвращает `true`, если клиент подключен к серверу.

### `GetVersion() string`
Возвращает текущую версию SDK. При сборке через GitHub Release возвращает версию (например, `v0.3.2`), при локальной сборке - `dev version`.
