# entities

entities - catalog service платформы Overmindv. Он владеет университетами, программами, курсами, темами, иерархией тем и prerequisites.

## Функционал

- создание, чтение, обновление и soft delete университетов;
- создание, чтение, обновление и soft delete программ;
- создание, чтение, обновление и soft delete курсов;
- создание, чтение, обновление и soft delete тем;
- изменение status catalog-сущностей;
- построение дерева тем;
- добавление и удаление prerequisites;
- проверка существования сущностей и связки `university -> program -> course -> topic`;
- запись catalog events в `outbox_events`.

Изменение каталога и вставка события в `outbox_events` фиксируются одной
транзакцией PostgreSQL. При ошибке outbox запись каталога откатывается.
Kafka работает отдельно: сбой брокера не откатывает уже сохраненное событие.

## Снимок каталога

Задайте `ENTITIES_SNAPSHOT_TOKEN` в окружении Entities и передавайте его как
`Authorization: Bearer <token>`. Без токена маршруты не регистрируются.

- `GET /v1/internal/catalog/snapshot?limit=100` начинает выгрузку. Следующую
  страницу запрашивают с полученными `watermark` и `next_cursor` (параметр
  `cursor`). Если `next_cursor` не пришел, снимок завершен.
- `GET /v1/internal/catalog/changes?after=<watermark>&limit=100` возвращает
  изменения графа после снимка. Для следующей страницы передайте `after=through`
  и сохраните `watermark` ответа. При `more: false` догнали эту версию.

В `catalog.activity.v1` добавлен `event_id`, равный ID строки outbox. У новых
событий также есть `catalog_revision` и `aggregate_version`, у изменений графа
есть `graph_change`. Старые поля, payload и ключ Kafka не поменялись.
Снимок и replay возвращают темы и пререквизиты, в том числе tombstone. Удаление
темы не удаляет ее связи из канонического каталога: потребитель исключает
связи с удаленной темой из рабочего графа.

При обновлении работающего окружения сначала выкатите атомарную запись из
OVM-131. Старые экземпляры, которые пишут каталог и outbox раздельно, нужно
остановить до применения миграции снимка.

## Оценка времени и результаты обучения

При создании и обновлении темы можно передать `learning_metadata`:

```json
{
  "estimated_minutes": 90,
  "outcomes": ["Оценить временную сложность фрагмента кода"]
}
```

На чтении отсутствие объекта означает, что данных нет. Время можно не
указывать. Изменение темы без `learning_metadata` или со значением `null`
сохраняет прежние данные. Переданный объект заменяет их целиком; пустой
объект очищает. Те же данные возвращаются в снимке и новых событиях темы.

Пилотный [курс алгоритмов](examples/pilot/algorithms-learning-metadata.json)
хранится отдельно от миграций. После согласования выберите один экземпляр
курса по ID образовательной программы, а не все курсы со slug `algorithms`.
Редактору с правом изменения каталога нужно прочитать текущие поля темы
и отправить их вместе с `learningMetadata` через `updateTopic` в Gateway.
Слоты времени в файле не являются обязательной длительностью для студента.

## Бизнес-логика

entities не хранит пользователей и роли. Write-операции доступны только actor с ролью `admin` или `superuser`, которую проверяет api-gateway по JWT Users и передаёт в entities через internal headers.

Связи `program.university_id`, `course.program_id` и `topic.course_id` могут быть пустыми. Это backlog-режим: сущность уже создана, но ещё не привязана к родителю. Пустые optional UUID нормализуются в `nil`, чтобы PostgreSQL не получал пустые строки вместо UUID.

Родительская тема должна принадлежать тому же курсу, что и дочерняя тема. Prerequisite тоже разрешён только между темами одного курса. Циклы в иерархии тем и графе prerequisites запрещены.

## Запуск

Локально сервис запускается в составе общего окружения из `infra`:

```bash
cd ../infra
cp .env.example .env
make up
```

Для разработки самого сервиса:

```bash
make test
make lint
make build
```

Чтобы проверить откат, примените миграции к выделенной БД с суффиксом
`_test` и передайте ее DSN в `ENTITIES_TEST_DSN`:

```bash
go test -race ./internal/repository -run TestPostgresCatalogOutboxRollback -count=1
```

Миграции на локальной БД:

```bash
make migrate-up DATABASE_URL='postgres://entities:entities@localhost:5432/entities?sslmode=disable'
```

HTTP API entities является внутренним API для api-gateway. Frontend должен обращаться только к GraphQL endpoint api-gateway.
