# Ironhide

Ironhide - catalog service платформы Overmindv. Он владеет университетами, программами, курсами, темами, иерархией тем и prerequisites.

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

Ironhide не хранит пользователей и роли. Write-операции доступны только actor с ролью `admin` или `superuser`, которую проверяет Laserbeak по JWT Arcee и передаёт в Ironhide через internal headers.

Связи `program.university_id`, `course.program_id` и `topic.course_id` могут быть пустыми. Это backlog-режим: сущность уже создана, но ещё не привязана к родителю. Пустые optional UUID нормализуются в `nil`, чтобы PostgreSQL не получал пустые строки вместо UUID.

Родительская тема должна принадлежать тому же курсу, что и дочерняя тема. Prerequisite тоже разрешён только между темами одного курса. Циклы в иерархии тем и графе prerequisites запрещены.

## Запуск

Локально сервис запускается в составе общего окружения из `ratchet`:

```bash
cd ../ratchet
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
make migrate-up DATABASE_URL='postgres://ironhide:ironhide@localhost:5432/ironhide?sslmode=disable'
```

HTTP API Ironhide является внутренним API для Laserbeak. Frontend должен обращаться только к GraphQL endpoint Laserbeak.
