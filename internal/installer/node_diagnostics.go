package installer

import (
	"github.com/ConsolePro/RemnaManager/internal/services"
	"strings"
)

func nodeAgentFailure(data []byte) error {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "RM_NODE_FAILURE=") {
			continue
		}
		code := strings.TrimSpace(strings.TrimPrefix(line, "RM_NODE_FAILURE="))
		messages := map[string][2]string{
			"preflight":        {"Проверка сервера ноды не завершена. Проверьте DNS, SSH и отсутствие чужой установки; подробности в журнале.", "Node preflight failed. Check DNS, SSH and foreign installations; inspect the log."},
			"dependencies":     {"Не удалось установить зависимости или запустить Docker. Проверьте apt и доступ к репозиториям; подробности в журнале.", "Dependency installation or Docker startup failed. Check apt and repository connectivity; inspect the log."},
			"image-download":   {"Не удалось загрузить или закрепить образ ноды. Проверьте доступ Docker к реестру образов.", "Could not download or pin the node image. Check Docker registry connectivity."},
			"site-files":       {"Не удалось установить файлы сайта. Проверьте свободное место и права на /var/www/remnamanager-node.", "Could not install website files. Check disk space and /var/www/remnamanager-node permissions."},
			"firewall-trial":   {"Пробное применение firewall не завершилось. Проверьте nftables и systemd; правила не подтверждены.", "Firewall trial failed. Check nftables and systemd; rules were not confirmed."},
			"firewall-confirm": {"Не удалось подтвердить firewall. Если 120 секунд истекли, продолжите установку для новой проверки SSH.", "Could not confirm firewall. If 120 seconds expired, resume installation for a new SSH trial."},
			"acme-download":    {"Не удалось скачать ACME-клиент. Проверьте исходящий HTTPS к raw.githubusercontent.com.", "Could not download the ACME client. Check outbound HTTPS to raw.githubusercontent.com."},
			"acme-checksum":    {"ACME-клиент не прошёл проверку SHA256. Код клиента не запускался. Обновите менеджер и повторите установку; проверку целостности отключать не нужно.", "ACME client SHA256 verification failed. The client was not executed. Update the manager and retry; do not disable integrity checks."},
			"acme-account":     {"Не удалось зарегистрировать аккаунт ACME. Проверьте email и доступ к Let's Encrypt; подробности в журнале ноды.", "ACME account registration failed. Check email and Let's Encrypt connectivity; inspect the node log."},
			"acme-issue":       {"Не удалось выпустить сертификат. Проверьте A/AAAA домена, доступность TCP 80 извне и отсутствие CDN-проксирования. Точная ошибка центра сертификации — в журнале ноды.", "Certificate issuance failed. Check domain A/AAAA records, public TCP 80 and disabled CDN proxying. The CA error is in the node log."},
			"acme-install":     {"Не удалось установить сертификат или выполнить reload Nginx. Проверьте журнал ноды и nginx -t.", "Certificate installation or Nginx reload failed. Check the node log and nginx -t."},
			"acme-timer":       {"Не удалось включить задачу обновления сертификата. Проверьте systemd на ноде.", "Could not enable certificate renewal. Check node systemd."},
			"nginx-config":     {"Конфигурация Nginx не прошла проверку. Выполните nginx -t на ноде и проверьте журнал.", "Nginx configuration validation failed. Run nginx -t on the node and check its log."},
			"nginx-start":      {"Не удалось запустить Nginx. Проверьте TCP 80 и systemctl status nginx на ноде.", "Could not start Nginx. Check TCP 80 and systemctl status nginx on the node."},
			"nginx-https":      {"Не удалось включить HTTPS в Nginx. Проверьте сертификат и nginx -t на ноде.", "Could not activate Nginx HTTPS. Check the certificate and nginx -t on the node."},
			"node-start":       {"Не удалось запустить контейнер ноды. Проверьте docker compose ps и логи в /opt/remnanode.", "Could not start the node container. Check docker compose ps and logs in /opt/remnanode."},
			"compose-config":   {"Compose-конфигурация ноды не прошла проверку. Подробности в журнале ноды.", "Node Compose configuration validation failed. Inspect the node log."},
			"firewall":         {"Не удалось проверить или применить firewall ноды. Подробности в журнале; чужие правила автоматически не очищаются.", "Could not check or apply node firewall. Inspect the log; foreign rules are not automatically cleared."},
		}
		if message, ok := messages[code]; ok {
			return services.LocalizedError(message[0], message[1])
		}
	}
	return nil
}
