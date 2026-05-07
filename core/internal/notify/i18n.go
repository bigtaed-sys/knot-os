package notify

import (
	"fmt"
	"strings"
)

// L10n holds a phrase dictionary per language. Phrases are looked
// up by stable string keys; missing keys fall back to the English
// dictionary, then to the key itself, so a typo in a handler
// surfaces instead of crashing.
//
// Phrase values can be Go format strings; T() accepts variadic
// args and runs fmt.Sprintf when args are non-empty.
type L10n struct {
	primary string
	tables  map[string]map[string]string
}

// newL10n constructs the bot's translation tables. Compact: every
// phrase the bot ever shows lives in this one file, in both
// languages, side-by-side. Adding a phrase = one entry in each
// map, no string-extraction tooling needed at v0.4 scale.
func newL10n(primary string) *L10n {
	if primary != "ru" && primary != "en" {
		primary = "ru"
	}
	return &L10n{
		primary: primary,
		tables: map[string]map[string]string{
			"ru": phrasesRU,
			"en": phrasesEN,
		},
	}
}

// T returns the localized phrase for key in the given language.
// Falls back to English, then the key itself, so a missing
// translation surfaces as a string the developer can grep for.
func (l *L10n) T(lang, key string, args ...any) string {
	if lang == "" {
		lang = l.primary
	}
	tab, ok := l.tables[lang]
	if !ok {
		tab = l.tables["en"]
	}
	val, ok := tab[key]
	if !ok {
		// English fallback before giving up.
		if lang != "en" {
			if alt, ok := l.tables["en"][key]; ok {
				val = alt
			}
		}
	}
	if val == "" {
		val = key
	}
	if len(args) == 0 {
		return val
	}
	return fmt.Sprintf(val, args...)
}

// Joined renders a single string with one phrase per line. Used
// by the multi-line status messages where each line is its own
// translation key.
func (l *L10n) Joined(lang string, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, l.T(lang, k))
	}
	return strings.Join(parts, "\n")
}

var phrasesRU = map[string]string{
	"welcome":          "Привет! 👋 Я — бот KnotOS, ваш карманный пульт от домашнего роутера.\n\nЧерез меня можно глянуть, что творится в сети, разбудить компьютер, выдать гостям Wi-Fi и многое другое.",
	"need_pin":         "🔐 Сначала давайте познакомимся.\n\nОткройте админку → *Система* → *Уведомления*, нажмите «Привязать Telegram» и пришлите мне 6-значный PIN. Это займёт 10 секунд.",
	"pin_ok":           "Готово! 🎉 Рад знакомству, %s.\n\nНаберите /help — расскажу, что умею. Или сразу /status — посмотрим, как там сеть.",
	"pin_bad":          "🤔 Что-то PIN не подходит — или код истёк, или цифры разошлись.\n\nЗайдите в админку и сгенерируйте новый, я подожду.",
	"unauth":           "🔒 Мы пока не знакомы — этот чат не привязан к роутеру.\n\nОткройте админку → *Система* → *Уведомления*, чтобы привязать.",
	"help":             "Я умею вот что:\n\n📊 /status — что сейчас с сетью\n🖥 /devices — кто подключён, можно разбудить или сменить профиль\n🛡 /protection — статистика блокировок DNS\n🌐 /routing — VPN-маршрутизация по устройствам\n👥 /guest — гостевая сеть\n🌍 /lang — язык бота\n💡 /tips — пара советов\n👋 /unlink — отвязать этот чат\n\nТакже я сам напишу, если что-то важное случится — новое устройство в сети, WAN потерялся, обновление вышло.",
	"unknown_command":  "🤷 Не знаю такой команды. Наберите /help — там все, что я умею.",
	"choose_lang":      "🌍 На каком языке вам удобнее?",
	"lang_set":         "Отлично, говорим по-русски ✓",
	"unlink_done":      "👋 Отвязали. Чтобы вернуться — сгенерируйте новый PIN в админке. Берегите сеть!",
	"tips":             "💡 *Маленькие фишки KnotOS*\n\n• На /devices нажмите устройство — там кнопки «Разбудить» и «Сменить профиль».\n• /routing покажет, через какой VPN-сервер кто ходит. Серверы добавляются в админке.\n• /protection даёт переключить DNS на DoH одним тапом — приватнее, чем UDP.\n• Бот сам пришлёт уведомление, если в сеть зашло новое устройство, или если WAN отвалился.\n• Гостевая сеть закрывается автоматически по таймеру — но можно и вручную через /guest.",

	// Status
	"status_title":   "📊 *Сводка по сети*",
	"status_role":    "🎩 Роль: %s",
	"status_device":  "🏷 Имя: %s",
	"status_version": "📦 Версия: %s",
	"status_wan":     "🌐 WAN: %s",
	"status_ap":      "📶 AP «%s»: %s",
	"status_clients": "🖥 В сети сейчас: *%d*",
	"role_setup":     "режим настройки",
	"role_extender": "Wi-Fi повторитель",
	"role_router":   "Wi-Fi роутер",
	"up":             "🟢 работает",
	"down":           "🔴 нет связи",

	// Devices list
	"devices_title":     "🖥 *Устройства в сети*",
	"devices_empty":     "Пока пусто 🌫 — никто ещё не подключался.\n\nКогда телефон или ноутбук получит Wi-Fi, он появится здесь.",
	"device_online":     "🟢",
	"device_offline":    "⚫",
	"device_stale":      "🕓",
	"device_menu_title": "*%s*\n\n🆔 MAC: `%s`\n🌐 IP: %s\n🛡 Профиль: %s",
	"profile_none":      "не задан",
	"button_back":       "« Назад",
	"button_wake":       "🔌 Разбудить (Wake-on-LAN)",
	"button_set_profile": "🛡 Сменить профиль",
	"button_pick":       "✓ Выбрать",
	"wake_sent":         "📡 Magic-пакет улетел. Если WoL включён в BIOS — компьютер проснётся через несколько секунд.",
	"wake_offline_only": "🤔 Будить можно только то, что сейчас спит. Этот девайс уже онлайн.",
	"profile_picker":    "🛡 Какой профиль присвоить «%s»?",
	"profile_set_ok":    "Профиль теперь — %s ✓",

	// Protection
	"protection_title":   "🛡 *Защита DNS*",
	"protection_queries": "📈 Запросов всего: *%d*",
	"protection_blocked": "🚫 Заблокировано: *%d* (%s%%)",
	"protection_mode":    "🔌 Транспорт: %s",
	"upstream_udp":       "UDP (классика)",
	"upstream_doh":       "DoH 🔒 (через HTTPS, приватнее)",
	"button_dns_doh":     "🔒 Переключить на DoH",
	"button_dns_udp":     "🔁 Вернуть UDP",
	"dns_switched":       "Транспорт DNS теперь — %s",

	// Routing (M30)
	"routing_title":   "🌐 *VPN-маршрутизация*",
	"routing_empty":   "Подписок пока не добавлено. Откройте админку → *Маршрутизация*, чтобы вставить ссылку на подписку или одиночный vless://.",
	"routing_subs":    "📡 Подписок: *%d*  ·  серверов: *%d*",
	"routing_buckets": "🚦 Через тоннель: *%d*  ·  напрямую: *%d*  ·  заблокировано (kill switch): *%d*",
	"routing_missing": "⚠️ Серверов недоступно: *%d* — откройте админку и обновите подписку либо выберите другой сервер.",

	// Guest
	"guest_title":       "👥 *Гостевая сеть*",
	"guest_none":        "Сейчас гостевой сети нет 🌫\n\nЕсли к вам пришли друзья — откройте админку → *Гости*, нажмите «Создать», и получится одноразовый QR-код.",
	"guest_active":      "📶 SSID: `%s`\n🔑 Пароль: `%s`\n⏳ Осталось: %s\n\nПокажите гостям QR из админки — они подключатся в одно касание.",
	"guest_forever":     "до отмены",
	"button_guest_revoke": "✗ Закрыть прямо сейчас",
	"guest_revoked":     "👋 Гостевая сеть закрыта. Все гости отключены.",

	// Notification messages (push from event bus)
	"notif_wan_up":             "🟢 *Интернет вернулся* — %s (%s)\n\nСеть снова работает.",
	"notif_wan_down":           "🔴 *Пропал интернет* — %s\n\nПроверю позже и сообщу, как восстановится.",
	"notif_device_joined":      "👋 *Новое устройство* в сети!\n\n🏷 %s\n🆔 `%s`\n🌐 %s\n\nЕсли не узнаёте — загляните в /devices и проверьте.",
	"notif_guest_created":      "👥 Гостевая сеть `%s` поднята ✨",
	"notif_guest_revoked":      "👥 Гостевая сеть `%s` закрыта.",
	"notif_guest_expired":      "👥 Гостевая сеть `%s` сама истекла по таймеру 🕓",
	"notif_update_available":   "✨ *Доступно обновление KnotOS*\n\nТекущая версия: %s\nНовая: *%s*\n\nЗайдите в админку → *Система* → *Обновления*, чтобы установить. Это займёт пару минут.",
	"notif_profile_changed":    "🛡 Профиль *%s* присвоен устройству *%s* (было: %s).",
}

var phrasesEN = map[string]string{
	"welcome":          "Hi there! 👋 I'm the KnotOS bot — your pocket remote for the home router.\n\nThrough me you can peek at the network, wake a sleeping computer, hand out guest Wi-Fi, and a lot more.",
	"need_pin":         "🔐 Let's get acquainted first.\n\nOpen the admin UI → *System* → *Notifications*, click \"Link Telegram\", and send me the 6-digit PIN. Takes 10 seconds.",
	"pin_ok":           "All set! 🎉 Nice to meet you, %s.\n\nType /help for the full menu, or jump straight to /status to see how the network's doing.",
	"pin_bad":          "🤔 That PIN doesn't quite work — either it's wrong or it's expired.\n\nPop into the admin UI and generate a new one — I'll be right here.",
	"unauth":           "🔒 We haven't met yet — this chat isn't linked to a router.\n\nOpen the admin UI → *System* → *Notifications* to link it.",
	"help":             "Here's what I can do:\n\n📊 /status — current network state\n🖥 /devices — who's connected, wake or reassign profiles\n🛡 /protection — DNS-blocking stats\n🌐 /routing — per-device VPN routing\n👥 /guest — guest network\n🌍 /lang — bot language\n💡 /tips — a few hidden tricks\n👋 /unlink — disconnect this chat\n\nI'll also ping you when something matters — a new device joins, the WAN goes down, an update lands.",
	"unknown_command":  "🤷 I don't know that one. /help has the full list.",
	"choose_lang":      "🌍 Which language do you prefer?",
	"lang_set":         "Got it — speaking English ✓",
	"unlink_done":      "👋 Unlinked. To come back, generate a fresh PIN in the admin UI. Take care of that network!",
	"tips":             "💡 *A few KnotOS tricks*\n\n• In /devices, tap any row — you get «Wake» and «Change profile» buttons.\n• /routing shows which device goes through which VPN server. Add servers in the admin UI.\n• /protection lets you flip DNS to DoH in one tap — more private than UDP.\n• I'll ping you when a new device joins or when the WAN drops.\n• Guest network expires on its own — but you can also close it manually via /guest.",

	// Status
	"status_title":   "📊 *Network snapshot*",
	"status_role":    "🎩 Role: %s",
	"status_device":  "🏷 Name: %s",
	"status_version": "📦 Version: %s",
	"status_wan":     "🌐 WAN: %s",
	"status_ap":      "📶 AP «%s»: %s",
	"status_clients": "🖥 Online right now: *%d*",
	"role_setup":     "setup mode",
	"role_extender": "Wi-Fi extender",
	"role_router":   "Wi-Fi router",
	"up":             "🟢 up",
	"down":           "🔴 down",

	// Devices list
	"devices_title":     "🖥 *Devices on the network*",
	"devices_empty":     "Nothing yet 🌫 — no one has connected.\n\nAs soon as a phone or laptop grabs Wi-Fi, it'll show up here.",
	"device_online":     "🟢",
	"device_offline":    "⚫",
	"device_stale":      "🕓",
	"device_menu_title": "*%s*\n\n🆔 MAC: `%s`\n🌐 IP: %s\n🛡 Profile: %s",
	"profile_none":      "not set",
	"button_back":       "« Back",
	"button_wake":       "🔌 Wake (Wake-on-LAN)",
	"button_set_profile": "🛡 Change profile",
	"button_pick":       "✓ Pick",
	"wake_sent":         "📡 Magic packet's away. If WoL is enabled in BIOS, the machine wakes within seconds.",
	"wake_offline_only": "🤔 Wake only works on sleeping devices. This one is already online.",
	"profile_picker":    "🛡 Which profile should «%s» have?",
	"profile_set_ok":    "Profile is now %s ✓",

	// Protection
	"protection_title":   "🛡 *DNS protection*",
	"protection_queries": "📈 Total queries: *%d*",
	"protection_blocked": "🚫 Blocked: *%d* (%s%%)",
	"protection_mode":    "🔌 Transport: %s",
	"upstream_udp":       "UDP (classic)",
	"upstream_doh":       "DoH 🔒 (over HTTPS, more private)",
	"button_dns_doh":     "🔒 Switch to DoH",
	"button_dns_udp":     "🔁 Back to UDP",
	"dns_switched":       "DNS transport is now — %s",

	// Routing (M30)
	"routing_title":   "🌐 *VPN routing*",
	"routing_empty":   "No subscriptions yet. Open the admin UI → *Routing* to paste a subscription URL or a single vless://.",
	"routing_subs":    "📡 Subscriptions: *%d*  ·  servers: *%d*",
	"routing_buckets": "🚦 Tunneling: *%d*  ·  direct: *%d*  ·  kill-switched: *%d*",
	"routing_missing": "⚠️ %d server(s) unreachable — open the admin UI and refresh the subscription, or pick another server.",

	// Guest
	"guest_title":       "👥 *Guest network*",
	"guest_none":        "No guest network right now 🌫\n\nIf friends are over — open the admin UI → *Guests*, click «Create», and you'll get a one-time QR code.",
	"guest_active":      "📶 SSID: `%s`\n🔑 Password: `%s`\n⏳ Remaining: %s\n\nShow your guests the QR from the admin UI — they connect in one tap.",
	"guest_forever":     "until revoked",
	"button_guest_revoke": "✗ Close it now",
	"guest_revoked":     "👋 Guest network closed. All guests disconnected.",

	// Notification messages (push from event bus)
	"notif_wan_up":             "🟢 *Internet's back* — %s (%s)\n\nNetwork is up again.",
	"notif_wan_down":           "🔴 *Internet dropped* — %s\n\nI'll keep an eye on it and let you know when it's back.",
	"notif_device_joined":      "👋 *New device* on the network!\n\n🏷 %s\n🆔 `%s`\n🌐 %s\n\nIf this isn't familiar, peek at /devices.",
	"notif_guest_created":      "👥 Guest network `%s` is up ✨",
	"notif_guest_revoked":      "👥 Guest network `%s` closed.",
	"notif_guest_expired":      "👥 Guest network `%s` expired on its own 🕓",
	"notif_update_available":   "✨ *KnotOS update is out*\n\nCurrent: %s\nNew: *%s*\n\nOpen the admin UI → *System* → *Updates* to install. Takes a couple of minutes.",
	"notif_profile_changed":    "🛡 Profile *%s* assigned to *%s* (was: %s).",
}
