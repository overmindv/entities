package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

// Postgres реализует storage contract Entities поверх PostgreSQL.
type Postgres struct {
	pool        *pgxpool.Pool
	publicReads bool
}

// New создаёт PostgreSQL repository с готовым pgx pool.
func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{
		pool: pool,
	}
}

func (r *Postgres) PublicReads() *Postgres {
	return &Postgres{
		pool:        r.pool,
		publicReads: true,
	}
}

func (r *Postgres) readTable(name string) string {
	if r.publicReads {
		return "catalog_public_" + name
	}

	return name
}

// Ping проверяет доступность PostgreSQL.
func (r *Postgres) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// CreateUniversity создаёт университет и outbox event.
func (r *Postgres) CreateUniversity(ctx context.Context, item domain.University) (domain.University, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.University, error) {
		row := tx.QueryRow(ctx, `
	        INSERT INTO universities (name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by)
	        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
	        RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`,
			item.Name, item.ShortName, item.City, item.Country, item.WebsiteURL, item.LogoFileID, item.Status, item.CreatedBy)
		result, err := scanUniversity(row)
		if err != nil {
			return domain.University{}, translateConstraint(err, apperror.UniversityAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.university.created", "university", result.ID, item.CreatedBy, result)
	})
}

// GetUniversity возвращает активный университет по ID.
func (r *Postgres) GetUniversity(ctx context.Context, id string) (domain.University, error) {
	row := r.pool.QueryRow(ctx, `SELECT id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at FROM `+r.readTable("universities")+` WHERE id = $1 AND deleted_at IS NULL`, id)
	item, err := scanUniversity(row)

	return item, translateNotFound(err, apperror.UniversityNotFound)
}

// ListUniversities возвращает активные университеты с фильтрацией через Jet query builder.
func (r *Postgres) ListUniversities(ctx context.Context, options domain.ListOptions) ([]domain.University, error) {
	id := postgres.StringColumn("id")
	name := postgres.StringColumn("name")
	shortName := postgres.StringColumn("short_name")
	city := postgres.StringColumn("city")
	country := postgres.StringColumn("country")
	websiteURL := postgres.StringColumn("website_url")
	logoFileID := postgres.StringColumn("logo_file_id")
	status := postgres.StringColumn("status")
	createdBy := postgres.StringColumn("created_by")
	updatedBy := postgres.StringColumn("updated_by")
	createdAt := postgres.TimestampzColumn("created_at")
	updatedAt := postgres.TimestampzColumn("updated_at")
	deletedAt := postgres.TimestampzColumn("deleted_at")
	table := postgres.NewTable("", r.readTable("universities"), "", id, name, shortName, city, country, websiteURL, logoFileID, status, createdBy, updatedBy, createdAt, updatedAt, deletedAt)
	condition := deletedAt.IS_NULL()
	if options.Status != "" {
		condition = condition.AND(status.EQ(postgres.String(options.Status)))
	}
	if options.Search != "" {
		pattern := postgres.String("%" + options.Search + "%")
		condition = condition.AND(postgres.LOWER(name).LIKE(postgres.LOWER(pattern)).OR(postgres.LOWER(shortName).LIKE(postgres.LOWER(pattern))))
	}
	statement := postgres.SELECT(id, name, shortName, city, country, websiteURL, logoFileID, status, createdBy, updatedBy, createdAt, updatedAt, deletedAt).
		FROM(table).
		WHERE(condition).
		ORDER_BY(name.ASC(), id.ASC()).
		LIMIT(int64(options.Limit)).
		OFFSET(int64(options.Offset))
	query, args := statement.Sql()
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.University, 0)
	for rows.Next() {
		item, scanErr := scanUniversity(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

// UpdateUniversity обновляет университет и пишет outbox event.
func (r *Postgres) UpdateUniversity(ctx context.Context, item domain.University) (domain.University, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.University, error) {
		row := tx.QueryRow(ctx, `UPDATE universities SET name=$2, short_name=$3, city=$4, country=$5, website_url=$6, logo_file_id=$7, updated_by=$8, updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`, item.ID, item.Name, item.ShortName, item.City, item.Country, item.WebsiteURL, item.LogoFileID, item.UpdatedBy)
		result, err := scanUniversity(row)
		if err != nil {
			return domain.University{}, translateConstraint(translateNotFound(err, apperror.UniversityNotFound), apperror.UniversityAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.university.updated", "university", result.ID, item.UpdatedBy, result)
	})
}

// DeleteUniversity выполняет soft delete университета.
func (r *Postgres) DeleteUniversity(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "universities", "university", id, actorID)
}

// ChangeUniversityStatus меняет status университета и пишет outbox event.
func (r *Postgres) ChangeUniversityStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.University, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.University, error) {
		row := tx.QueryRow(ctx, `UPDATE universities SET status=$2, updated_by=$3, updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`, id, status, actorID)
		result, err := scanUniversity(row)
		if err != nil {
			return domain.University{}, translateConstraint(translateNotFound(err, apperror.UniversityNotFound), apperror.UniversityAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.university.updated", "university", id, actorID, result)
	})
}

// CreateProgram создаёт программу, если parent university существует или не задан.
func (r *Postgres) CreateProgram(ctx context.Context, item domain.Program) (domain.Program, error) {
	if item.UniversityID != nil {
		exists, err := r.activeUniversityExists(ctx, *item.UniversityID)
		if err != nil {
			return domain.Program{}, err
		}
		if !exists {
			return domain.Program{}, apperror.New(apperror.UniversityNotFound, "университет не найден", 404)
		}
	}

	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Program, error) {
		row := tx.QueryRow(ctx, `INSERT INTO programs (university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.UniversityID, item.Name, item.ShortName, item.Faculty, item.DegreeLevel, item.StartYear, item.Status, item.CreatedBy)
		result, err := scanProgram(row)
		if err != nil {
			return domain.Program{}, translateConstraint(err, apperror.ProgramAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.program.created", "program", result.ID, item.CreatedBy, result)
	})
}

// GetProgram возвращает активную программу по ID.
func (r *Postgres) GetProgram(ctx context.Context, id string) (domain.Program, error) {
	item, err := scanProgram(r.pool.QueryRow(ctx, `SELECT id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at FROM `+r.readTable("programs")+` WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.ProgramNotFound)
}

// ListPrograms возвращает активные программы parent university или общий список.
func (r *Postgres) ListPrograms(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Program, error) {
	const selectProgram = `SELECT id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at FROM `
	query := selectProgram + r.readTable("programs") + ` WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name,id LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) != "" {
		query = selectProgram + r.readTable("programs") + ` WHERE university_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR name ILIKE '%'||$3||'%') ORDER BY name,id LIMIT $4 OFFSET $5`
		args = []any{parentID, options.Status, options.Search, options.Limit, options.Offset}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Program, 0)
	for rows.Next() {
		item, scanErr := scanProgram(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

// UpdateProgram обновляет программу и пишет outbox event.
func (r *Postgres) UpdateProgram(ctx context.Context, item domain.Program) (domain.Program, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Program, error) {
		row := tx.QueryRow(ctx, `UPDATE programs SET name=$2,short_name=$3,faculty=$4,degree_level=$5,start_year=$6,updated_by=$7,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ID, item.Name, item.ShortName, item.Faculty, item.DegreeLevel, item.StartYear, item.UpdatedBy)
		result, err := scanProgram(row)
		if err != nil {
			return domain.Program{}, translateConstraint(translateNotFound(err, apperror.ProgramNotFound), apperror.ProgramAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.program.updated", "program", result.ID, item.UpdatedBy, result)
	})
}

// DeleteProgram выполняет soft delete программы.
func (r *Postgres) DeleteProgram(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "programs", "program", id, actorID)
}

// ChangeProgramStatus меняет status программы и пишет outbox event.
func (r *Postgres) ChangeProgramStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Program, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Program, error) {
		result, err := scanProgram(tx.QueryRow(ctx, `UPDATE programs SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, id, status, actorID))
		if err != nil {
			return domain.Program{}, translateConstraint(translateNotFound(err, apperror.ProgramNotFound), apperror.ProgramAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.program.updated", "program", id, actorID, result)
	})
}

// CreateCourse создаёт курс, если parent program существует или не задан.
func (r *Postgres) CreateCourse(ctx context.Context, item domain.Course) (domain.Course, error) {
	if item.ProgramID != nil {
		exists, err := r.activeProgramExists(ctx, *item.ProgramID)
		if err != nil {
			return domain.Course{}, err
		}
		if !exists {
			return domain.Course{}, apperror.New(apperror.ProgramNotFound, "программа не найдена", 404)
		}
	}

	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Course, error) {
		row := tx.QueryRow(ctx, `INSERT INTO courses (program_id,name,slug,description,semester,year_number,status,created_by,updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ProgramID, item.Name, item.Slug, item.Description, item.Semester, item.YearNumber, item.Status, item.CreatedBy)
		result, err := scanCourse(row)
		if err != nil {
			return domain.Course{}, translateConstraint(err, apperror.CourseAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.course.created", "course", result.ID, item.CreatedBy, result)
	})
}

// GetCourse возвращает активный курс по ID.
func (r *Postgres) GetCourse(ctx context.Context, id string) (domain.Course, error) {
	item, err := scanCourse(r.pool.QueryRow(ctx, `SELECT id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at FROM `+r.readTable("courses")+` WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.CourseNotFound)
}

// ListCourses возвращает активные курсы parent program или общий список.
func (r *Postgres) ListCourses(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Course, error) {
	const selectCourse = `SELECT id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at FROM `
	query := selectCourse + r.readTable("courses") + ` WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name,id LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) != "" {
		query = selectCourse + r.readTable("courses") + ` WHERE program_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR name ILIKE '%'||$3||'%') ORDER BY name,id LIMIT $4 OFFSET $5`
		args = []any{parentID, options.Status, options.Search, options.Limit, options.Offset}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Course, 0)
	for rows.Next() {
		item, scanErr := scanCourse(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

// UpdateCourse обновляет курс и пишет outbox event.
func (r *Postgres) UpdateCourse(ctx context.Context, item domain.Course) (domain.Course, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Course, error) {
		result, err := scanCourse(tx.QueryRow(ctx, `UPDATE courses SET name=$2,slug=$3,description=$4,semester=$5,year_number=$6,updated_by=$7,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ID, item.Name, item.Slug, item.Description, item.Semester, item.YearNumber, item.UpdatedBy))
		if err != nil {
			return domain.Course{}, translateConstraint(translateNotFound(err, apperror.CourseNotFound), apperror.CourseAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.course.updated", "course", result.ID, item.UpdatedBy, result)
	})
}

// DeleteCourse выполняет soft delete курса.
func (r *Postgres) DeleteCourse(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "courses", "course", id, actorID)
}

// ChangeCourseStatus меняет status курса и пишет outbox event.
func (r *Postgres) ChangeCourseStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Course, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Course, error) {
		result, err := scanCourse(tx.QueryRow(ctx, `UPDATE courses SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, id, status, actorID))
		if err != nil {
			return domain.Course{}, translateConstraint(translateNotFound(err, apperror.CourseNotFound), apperror.CourseAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.course.updated", "course", id, actorID, result)
	})
}

const topicColumns = `id,course_id,parent_topic_id,title,slug,description,order_index,
	difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at,estimated_study_minutes,learning_outcomes`

// CreateTopic создает тему, если курс существует или не задан.
func (r *Postgres) CreateTopic(ctx context.Context, item domain.Topic) (domain.Topic, error) {
	if item.CourseID != nil {
		exists, err := r.activeCourseExists(ctx, *item.CourseID)
		if err != nil {
			return domain.Topic{}, err
		}
		if !exists {
			return domain.Topic{}, apperror.New(apperror.CourseNotFound, "курс не найден", 404)
		}
	}

	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Topic, error) {
		minutes, outcomes := topicLearningValues(item.LearningMetadata)
		row := tx.QueryRow(ctx, `
			INSERT INTO topics (course_id,parent_topic_id,title,slug,description,order_index,difficulty,
				status,created_by,updated_by,estimated_study_minutes,learning_outcomes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10,COALESCE($11::text[],'{}'))
			RETURNING `+topicColumns, item.CourseID, item.ParentTopicID, item.Title, item.Slug,
			item.Description, item.OrderIndex, item.Difficulty, item.Status, item.CreatedBy, minutes, outcomes)
		result, err := scanTopic(row)
		if err != nil {
			return domain.Topic{}, translateConstraint(err, apperror.TopicAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.topic.created", "topic", result.ID, item.CreatedBy, result)
	})
}

// GetTopic возвращает активную тему по ID.
func (r *Postgres) GetTopic(ctx context.Context, id string) (domain.Topic, error) {
	item, err := scanTopic(r.pool.QueryRow(ctx, `SELECT `+topicColumns+` FROM `+r.readTable("topics")+` WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.TopicNotFound)
}

// ListTopics возвращает активные темы parent course или общий список.
func (r *Postgres) ListTopics(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Topic, error) {
	const selectTopic = `SELECT ` + topicColumns + ` FROM `
	query := selectTopic + r.readTable("topics") + ` t WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR title ILIKE '%'||$2||'%') ORDER BY order_index,title,id LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) == "" && !r.publicReads {
		query = `WITH page AS MATERIALIZED (
			SELECT id FROM topics
			WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR title ILIKE '%'||$2||'%')
			ORDER BY order_index,title,id LIMIT $3 OFFSET $4
		) ` + selectTopic + `page JOIN topics USING (id) ORDER BY order_index,title,id`
	} else if strings.TrimSpace(parentID) != "" {
		query = selectTopic + `topics t WHERE course_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR title ILIKE '%'||$3||'%') ORDER BY order_index,title,id LIMIT $4 OFFSET $5`
		if r.publicReads {
			// Обход от корней не включает циклы и потомков скрытого родителя.
			query = `WITH RECURSIVE visible AS (
				SELECT * FROM topics
				 WHERE course_id=$1 AND parent_topic_id IS NULL AND status='active' AND deleted_at IS NULL
				   AND EXISTS (SELECT 1 FROM catalog_public_courses WHERE id=$1)
				UNION ALL
				SELECT child.* FROM visible v JOIN LATERAL (
					SELECT * FROM topics
					 WHERE course_id=$1 AND parent_topic_id=v.id AND status='active' AND deleted_at IS NULL
					 OFFSET 0
				) child ON TRUE
			) ` + selectTopic + `visible t
				WHERE ($2='' OR $2='active') AND ($3='' OR t.title ILIKE '%'||$3||'%')
				ORDER BY t.order_index,t.title,t.id LIMIT $4 OFFSET $5`
		}
		args = []any{parentID, options.Status, options.Search, options.Limit, options.Offset}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Topic, 0)
	for rows.Next() {
		item, scanErr := scanTopic(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

// UpdateTopic обновляет тему и пишет outbox event.
func (r *Postgres) UpdateTopic(ctx context.Context, item domain.Topic) (domain.Topic, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Topic, error) {
		var courseID *string
		// Две темы одного курса не должны менять родителей по одному старому дереву.
		if err := tx.QueryRow(ctx, `
			SELECT course_id, pg_advisory_xact_lock(hashtextextended('entities:topic-tree:'||COALESCE(course_id::text,'backlog'),0))
			FROM topics WHERE id=$1 AND deleted_at IS NULL`, item.ID).Scan(&courseID, nil); err != nil {
			return domain.Topic{}, translateNotFound(err, apperror.TopicNotFound)
		}
		var oldParent *string
		if err := tx.QueryRow(ctx, `SELECT parent_topic_id FROM topics WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, item.ID).Scan(&oldParent); err != nil {
			return domain.Topic{}, translateNotFound(err, apperror.TopicNotFound)
		}
		moved := !sameOptionalString(oldParent, item.ParentTopicID)
		if moved && item.ParentTopicID != nil {
			if err := checkTopicMove(ctx, tx, item.ID, *item.ParentTopicID, courseID); err != nil {
				return domain.Topic{}, err
			}
		}
		minutes, outcomes := topicLearningValues(item.LearningMetadata)
		result, err := scanTopic(tx.QueryRow(ctx, `
			UPDATE topics SET parent_topic_id=$2,title=$3,slug=$4,description=$5,order_index=$6,
				difficulty=$7,updated_by=$8,updated_at=now(),
				estimated_study_minutes=CASE WHEN $9::boolean THEN $10::smallint ELSE estimated_study_minutes END,
				learning_outcomes=CASE WHEN $9::boolean THEN COALESCE($11::text[],'{}') ELSE learning_outcomes END
			WHERE id=$1 AND deleted_at IS NULL RETURNING `+topicColumns,
			item.ID, item.ParentTopicID, item.Title, item.Slug, item.Description, item.OrderIndex,
			item.Difficulty, item.UpdatedBy, item.LearningMetadata != nil, minutes, outcomes))
		if err != nil {
			return domain.Topic{}, translateConstraint(translateNotFound(err, apperror.TopicNotFound), apperror.TopicAlreadyExists)
		}

		return result, insertCatalogEventWithReset(ctx, tx, "catalog.topic.updated", "topic", result.ID,
			item.UpdatedBy, result, moved)
	})
}

// DeleteTopic выполняет soft delete темы.
func (r *Postgres) DeleteTopic(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "topics", "topic", id, actorID)
}

// ChangeTopicStatus меняет status темы и пишет outbox event.
func (r *Postgres) ChangeTopicStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Topic, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.Topic, error) {
		result, err := scanTopic(tx.QueryRow(ctx, `UPDATE topics SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING `+topicColumns, id, status, actorID))
		if err != nil {
			return domain.Topic{}, translateConstraint(translateNotFound(err, apperror.TopicNotFound), apperror.TopicAlreadyExists)
		}

		return result, insertCatalogEvent(ctx, tx, "catalog.topic.updated", "topic", id, actorID, result)
	})
}

// AddPrerequisite проверяет граф, добавляет связь и записывает событие.
func (r *Postgres) AddPrerequisite(ctx context.Context, item domain.TopicPrerequisite, actorID string) (domain.TopicPrerequisite, error) {
	return writeCatalog(ctx, r.pool, func(tx pgx.Tx) (domain.TopicPrerequisite, error) {
		courseID, err := lockPrerequisiteGraph(ctx, tx, item.TopicID)
		if err != nil {
			return domain.TopicPrerequisite{}, translateNotFound(err, apperror.TopicNotFound)
		}
		if checkErr := checkPrerequisite(ctx, tx, item, courseID); checkErr != nil {
			return domain.TopicPrerequisite{}, checkErr
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO topic_prerequisites (topic_id,prerequisite_topic_id) VALUES ($1,$2)
			RETURNING topic_id,prerequisite_topic_id,created_at`, item.TopicID, item.PrerequisiteTopicID).
			Scan(&item.TopicID, &item.PrerequisiteTopicID, &item.CreatedAt)
		if err != nil {
			return domain.TopicPrerequisite{}, translateConstraint(err, apperror.InvalidTopicPrerequisite)
		}

		return item, insertCatalogEvent(ctx, tx, "catalog.topic.prerequisite.added", "topic", item.TopicID, actorID, item)
	})
}

// RemovePrerequisite удаляет связь prerequisite и пишет outbox event.
func (r *Postgres) RemovePrerequisite(ctx context.Context, topicID, prerequisiteID, actorID string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := lockPrerequisiteGraph(ctx, tx, topicID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperror.New(apperror.InvalidTopicPrerequisite, "пререквизит не найден", 404)
			}

			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM topic_prerequisites WHERE topic_id=$1 AND prerequisite_topic_id=$2`, topicID, prerequisiteID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperror.New(apperror.InvalidTopicPrerequisite, "пререквизит не найден", 404)
		}

		return insertCatalogEvent(ctx, tx, "catalog.topic.prerequisite.removed", "topic", topicID, actorID, map[string]string{"prerequisite_topic_id": prerequisiteID})
	})
}

// ListPrerequisites возвращает prerequisites темы.
func (r *Postgres) ListPrerequisites(ctx context.Context, topicID string) ([]domain.TopicPrerequisite, error) {
	query := `SELECT edge.topic_id,edge.prerequisite_topic_id,edge.created_at FROM topic_prerequisites AS edge WHERE edge.topic_id=$1`
	if r.publicReads {
		query += ` AND EXISTS (SELECT 1 FROM catalog_public_topics t WHERE t.id = edge.topic_id)
		           AND EXISTS (SELECT 1 FROM catalog_public_topics t WHERE t.id = edge.prerequisite_topic_id)`
	}
	rows, err := r.pool.Query(ctx, query+` ORDER BY created_at,prerequisite_topic_id`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.TopicPrerequisite, 0)
	for rows.Next() {
		var item domain.TopicPrerequisite
		if err := rows.Scan(&item.TopicID, &item.PrerequisiteTopicID, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	return result, rows.Err()
}

// ValidateBinding проверяет существование и согласованность partial/full catalog binding.
func (r *Postgres) ValidateBinding(ctx context.Context, binding domain.Binding) (bool, error) {
	if binding.UniversityID != nil {
		exists, err := r.activeUniversityExists(ctx, *binding.UniversityID)
		if err != nil || !exists {
			return exists, err
		}
	}

	var programUniversityID *string
	if binding.ProgramID != nil {
		parentID, exists, err := r.activeProgramUniversityID(ctx, *binding.ProgramID)
		if err != nil || !exists {
			return exists, err
		}
		programUniversityID = parentID
		if binding.UniversityID != nil && !sameOptionalString(programUniversityID, binding.UniversityID) {
			return false, nil
		}
	}

	var courseProgramID *string
	if binding.CourseID != nil {
		parentID, exists, err := r.activeCourseProgramID(ctx, *binding.CourseID)
		if err != nil || !exists {
			return exists, err
		}
		courseProgramID = parentID
		if binding.ProgramID != nil && !sameOptionalString(courseProgramID, binding.ProgramID) {
			return false, nil
		}
	}

	if binding.TopicID != nil {
		parentID, exists, err := r.activeTopicCourseID(ctx, *binding.TopicID)
		if err != nil || !exists {
			return exists, err
		}
		if binding.CourseID != nil && !sameOptionalString(parentID, binding.CourseID) {
			return false, nil
		}
	}

	return true, nil
}

// activeUniversityExists проверяет, что университет существует и не удалён.
func (r *Postgres) activeUniversityExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+r.readTable("universities")+` WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)

	return exists, err
}

// activeProgramExists проверяет, что программа существует и не удалена.
func (r *Postgres) activeProgramExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM programs WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)

	return exists, err
}

// activeCourseExists проверяет, что курс существует и не удалён.
func (r *Postgres) activeCourseExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM courses WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)

	return exists, err
}

// activeProgramUniversityID возвращает university_id активной программы.
func (r *Postgres) activeProgramUniversityID(ctx context.Context, id string) (*string, bool, error) {
	var parentID *string
	if err := r.pool.QueryRow(ctx, `SELECT university_id FROM `+r.readTable("programs")+` WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}

		return nil, false, err
	}

	return parentID, true, nil
}

// activeCourseProgramID возвращает program_id активного курса.
func (r *Postgres) activeCourseProgramID(ctx context.Context, id string) (*string, bool, error) {
	var parentID *string
	if err := r.pool.QueryRow(ctx, `SELECT program_id FROM `+r.readTable("courses")+` WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}

		return nil, false, err
	}

	return parentID, true, nil
}

// activeTopicCourseID возвращает course_id активной темы.
func (r *Postgres) activeTopicCourseID(ctx context.Context, id string) (*string, bool, error) {
	var parentID *string
	if err := r.pool.QueryRow(ctx, `SELECT course_id FROM `+r.readTable("topics")+` WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}

		return nil, false, err
	}

	return parentID, true, nil
}

// sameOptionalString сравнивает nullable строки для проверки связей catalog hierarchy.
func sameOptionalString(left, right *string) bool {
	if left == nil || strings.TrimSpace(*left) == "" {
		return right == nil || strings.TrimSpace(*right) == ""
	}
	if right == nil || strings.TrimSpace(*right) == "" {
		return false
	}

	return *left == *right
}

// softDelete выполняет soft delete только для разрешённых catalog-таблиц и пишет outbox event.
func (r *Postgres) softDelete(ctx context.Context, table, aggregate, id, actorID string) error {
	allowed := map[string]string{"universities": apperror.UniversityNotFound, "programs": apperror.ProgramNotFound, "courses": apperror.CourseNotFound, "topics": apperror.TopicNotFound}
	code, ok := allowed[table]
	if !ok {
		return fmt.Errorf("unsupported table %q", table)
	}
	// table берётся только из allowlist, поэтому fmt.Sprintf не принимает пользовательский ввод.
	query := fmt.Sprintf("UPDATE %s SET deleted_at=now(),updated_at=now(),updated_by=$2 WHERE id=$1 AND deleted_at IS NULL", table)

	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, id, actorID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperror.New(code, "объект не найден", 404)
		}

		return insertCatalogEvent(ctx, tx, "catalog."+aggregate+".deleted", aggregate, id, actorID, map[string]string{"id": id})
	})
}

// scanner описывает общий contract pgx.Row и pgx.Rows для scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

// scanUniversity считывает строку PostgreSQL в domain.University.
func scanUniversity(row scanner) (domain.University, error) {
	var item domain.University
	err := row.Scan(&item.ID, &item.Name, &item.ShortName, &item.City, &item.Country, &item.WebsiteURL, &item.LogoFileID, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)

	return item, err
}

// scanProgram считывает строку PostgreSQL в domain.Program.
func scanProgram(row scanner) (domain.Program, error) {
	var item domain.Program
	err := row.Scan(&item.ID, &item.UniversityID, &item.Name, &item.ShortName, &item.Faculty, &item.DegreeLevel, &item.StartYear, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)

	return item, err
}

// scanCourse считывает строку PostgreSQL в domain.Course.
func scanCourse(row scanner) (domain.Course, error) {
	var item domain.Course
	err := row.Scan(&item.ID, &item.ProgramID, &item.Name, &item.Slug, &item.Description, &item.Semester, &item.YearNumber, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)

	return item, err
}

// scanTopic считывает строку PostgreSQL в domain.Topic.
func scanTopic(row scanner) (domain.Topic, error) {
	var item domain.Topic
	var metadata domain.TopicLearningMetadata
	err := row.Scan(&item.ID, &item.CourseID, &item.ParentTopicID, &item.Title, &item.Slug, &item.Description,
		&item.OrderIndex, &item.Difficulty, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt,
		&item.UpdatedAt, &item.DeletedAt, &metadata.EstimatedMinutes, &metadata.Outcomes)
	if metadata.EstimatedMinutes != nil || len(metadata.Outcomes) != 0 {
		item.LearningMetadata = &metadata
	}

	return item, err
}

func topicLearningValues(metadata *domain.TopicLearningMetadata) (*int, []string) {
	if metadata == nil {
		return nil, nil
	}

	return metadata.EstimatedMinutes, metadata.Outcomes
}

// translateNotFound преобразует pgx.ErrNoRows в публичную not-found ошибку Entities.
func translateNotFound(err error, code string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(code, "объект не найден", 404)
	}

	return err
}

// translateConstraint преобразует PostgreSQL constraint violations в публичную ошибку Entities.
func translateConstraint(err error, code string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23514" || pgErr.Code == "23503") {
		message := strings.TrimSpace(pgErr.ConstraintName)
		if message == "" {
			message = "нарушено ограничение данных"
		}

		return apperror.New(code, message, 409)
	}

	return err
}
