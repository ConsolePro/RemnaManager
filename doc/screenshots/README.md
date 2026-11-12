# Снимок терминала для README

`doc/assets/terminal.png` снят через VHS с реально работающего приложения в PTY Linux-контейнера. Используется деморежим: системные операции отключены, но ОС, CPU, RAM, диск, IP и аптайм собираются обычным кодом приложения. Метрики и экран не подменяются тестовой моделью.

Запись воспроизводится файлом [main.tape](main.tape). VHS запускает бинарник, ждёт обновления метрик и делает PNG-снимок браузерного терминала. Ручной отрисовки интерфейса нет.

Подготовка из корня репозитория на машине разработчика:

```bash
mkdir -p .test-state/readme-shot
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o .test-state/readme-shot/remanager ./cmd/remna
cp doc/screenshots/main.tape .test-state/readme-shot/main.tape
python3 - <<'PY'
import datetime, json, pathlib
path = pathlib.Path('.test-state/readme-shot')
path.joinpath('config.json').write_text(json.dumps({
    'language': 'ru', 'email': 'demo@example.com', 'initialized': True
}))
path.joinpath('update-cache.json').write_text(json.dumps({
    'tag_name': 'v' + pathlib.Path('VERSION').read_text().strip(),
    'checked': datetime.datetime.now(datetime.timezone.utc).isoformat()
}))
PY
docker run --rm --shm-size 256m \
  -v "$PWD/.test-state/readme-shot:/vhs" \
  ghcr.io/charmbracelet/vhs@sha256:ea49a6a1c529be83153e88321892b5585964418f0b9055e8c1e0d732194234a3 \
  /vhs/main.tape
cp .test-state/readme-shot/terminal.png doc/assets/terminal.png
```

Кэш проверки обновлений позволяет записывать экран без обращения к GitHub. Он не влияет на системные метрики. Временные конфигурация, бинарник и GIF остаются в `.test-state`, а в репозиторий добавляется только PNG и сценарий записи.
