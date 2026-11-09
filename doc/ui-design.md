# Обновлённый интерфейс

Основной фон — `#0F181F`, поверхность меню — `#19282F`, выбранная строка —
`#245E56`, акцент — `#42C79A`. Навигация выделяет строку целиком; статусы и
стрелки расположены справа. Ширина рабочей области ограничена 112 колонками.
При достаточной высоте пункты разделены дополнительными строками.

![Меню](previews/menu.png)

![Демонстрационная установка](previews/progress.png)

Это предпросмотры реального `Model.View()` с тестовыми метриками и доменами.
В работающем приложении данные поступают от системного сборщика.

## Графический логотип

Исходный PNG: `internal/ui/assets/logo.png`. Создан встроенным инструментом
`image_gen` с навыком imagegen, без CLI/API-ключа. Встроен через `go:embed`:
пользователю не требуется отдельный файл. Цветной знак выводится растровыми
полублоками Unicode, с уменьшением цветов средствами Lip Gloss для терминалов
без True Color. Системы Kitty/Sixel не требуются. Большой вариант — 12×6
терминальных ячеек, компактный — 4×2; при `NO_COLOR=1` используется контурный
знак без цветов.

Итоговый промпт генерации:

> Use case: logo-brand. Create a polished minimal brand icon for Remna Manager,
> a server management terminal application. A single bold geometric emerald green
> and mint monogram R, subtly formed from two connected server/network paths,
> in a dark midnight square (#10191f). Flat crisp pixel-friendly silhouette,
> generous negative space, centered symmetrical visual weight, clear even when
> reduced to a 16 by 16 pixel icon. Restrained modern infrastructure software
> aesthetic. No text, no letters other than the abstract R shape, no wordmark,
> no gradients, no glow, no shadows, no mockup, no border, no watermark.
> Square canvas. Save as a project-ready PNG.

## Обновление шапки

После каждого ответа сборщика Bubble Tea планирует новый сбор через секунду.
Сбор выполняется асинхронно и измеряет текущие CPU, RAM, SWAP, диск и интерфейсы;
он не использует снимок старта. Реальная частота — примерно 1.15 секунды с
учётом измерения CPU. Индикатор LIVE показывает время последнего ответа.
При задержке более 5 секунд показывается предупреждение; ошибки сборщика
помечаются как частичные данные. До первого ответа числовые нули не рисуются.

Проверки охватывают повторный запуск сбора в меню, форме и установке,
сохранность курсора/формы, предупреждение о задержке, PNG и монохромный режим,
размеры от 36×18 до 160×50. Клавиатурная навигация и подтверждение F1 сохранены.

Все этапы установки остаются демонстрационными. Статус online для нод не
рисуется: без подключения реального сервиса такой индикатор был бы вымышленным.
