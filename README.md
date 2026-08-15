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

Миграции на локальной БД:

```bash
make migrate-up DATABASE_URL='postgres://entities:entities@localhost:5432/entities?sslmode=disable'
```

HTTP API entities является внутренним API для api-gateway. Frontend должен обращаться только к GraphQL endpoint api-gateway.
