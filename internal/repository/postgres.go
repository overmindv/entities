package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/overmindv/ironhide/internal/apperror"
	"github.com/overmindv/ironhide/internal/domain"
)

// Postgres реализует storage contract Ironhide поверх PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// New создаёт PostgreSQL repository с готовым pgx pool.
func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{
		pool: pool,
	}
}

// Ping проверяет доступность PostgreSQL.
func (r *Postgres) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// CreateUniversity создаёт университет и outbox event.
func (r *Postgres) CreateUniversity(ctx context.Context, item domain.University) (domain.University, error) {
	row := r.pool.QueryRow(ctx, `
        INSERT INTO universities (name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
        RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`,
		item.Name, item.ShortName, item.City, item.Country, item.WebsiteURL, item.LogoFileID, item.Status, item.CreatedBy)
	result, err := scanUniversity(row)
	if err != nil {
		return domain.University{}, translateConstraint(err, apperror.UniversityAlreadyExists)
	}
	if err := r.event(ctx, "catalog.university.created", "university", result.ID, item.CreatedBy, result); err != nil {
		return domain.University{}, err
	}

	return result, nil
}

// GetUniversity возвращает активный университет по ID.
func (r *Postgres) GetUniversity(ctx context.Context, id string) (domain.University, error) {
	row := r.pool.QueryRow(ctx, `SELECT id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at FROM universities WHERE id = $1 AND deleted_at IS NULL`, id)
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
	table := postgres.NewTable("", "universities", "", id, name, shortName, city, country, websiteURL, logoFileID, status, createdBy, updatedBy, createdAt, updatedAt, deletedAt)
	condition := deletedAt.IS_NULL()
	if options.Status != "" {
		condition = condition.AND(status.EQ(postgres.String(options.Status)))
	}
	if options.Search != "" {
		pattern := postgres.String("%" + options.Search + "%")
		condition = condition.AND(name.LIKE(pattern).OR(shortName.LIKE(pattern)))
	}
	statement := postgres.SELECT(id, name, shortName, city, country, websiteURL, logoFileID, status, createdBy, updatedBy, createdAt, updatedAt, deletedAt).
		FROM(table).
		WHERE(condition).
		ORDER_BY(name.ASC()).
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
	row := r.pool.QueryRow(ctx, `UPDATE universities SET name=$2, short_name=$3, city=$4, country=$5, website_url=$6, logo_file_id=$7, updated_by=$8, updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`, item.ID, item.Name, item.ShortName, item.City, item.Country, item.WebsiteURL, item.LogoFileID, item.UpdatedBy)
	result, err := scanUniversity(row)
	if err != nil {
		return domain.University{}, translateConstraint(translateNotFound(err, apperror.UniversityNotFound), apperror.UniversityAlreadyExists)
	}
	err = r.event(ctx, "catalog.university.updated", "university", result.ID, item.UpdatedBy, result)

	return result, err
}

// DeleteUniversity выполняет soft delete университета.
func (r *Postgres) DeleteUniversity(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "universities", "university", id, actorID)
}

// ChangeUniversityStatus меняет status университета и пишет outbox event.
func (r *Postgres) ChangeUniversityStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.University, error) {
	row := r.pool.QueryRow(ctx, `UPDATE universities SET status=$2, updated_by=$3, updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id, name, short_name, city, country, website_url, logo_file_id, status, created_by, updated_by, created_at, updated_at, deleted_at`, id, status, actorID)
	result, err := scanUniversity(row)
	if err != nil {
		return domain.University{}, translateConstraint(translateNotFound(err, apperror.UniversityNotFound), apperror.UniversityAlreadyExists)
	}
	err = r.event(ctx, "catalog.university.updated", "university", id, actorID, result)

	return result, err
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

	row := r.pool.QueryRow(ctx, `INSERT INTO programs (university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.UniversityID, item.Name, item.ShortName, item.Faculty, item.DegreeLevel, item.StartYear, item.Status, item.CreatedBy)
	result, err := scanProgram(row)
	if err != nil {
		return domain.Program{}, translateConstraint(err, apperror.ProgramAlreadyExists)
	}
	err = r.event(ctx, "catalog.program.created", "program", result.ID, item.CreatedBy, result)

	return result, err
}

// GetProgram возвращает активную программу по ID.
func (r *Postgres) GetProgram(ctx context.Context, id string) (domain.Program, error) {
	item, err := scanProgram(r.pool.QueryRow(ctx, `SELECT id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at FROM programs WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.ProgramNotFound)
}

// ListPrograms возвращает активные программы parent university или общий список.
func (r *Postgres) ListPrograms(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Program, error) {
	query := `SELECT id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at FROM programs WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) != "" {
		query = `SELECT id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at FROM programs WHERE university_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR name ILIKE '%'||$3||'%') ORDER BY name LIMIT $4 OFFSET $5`
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
	row := r.pool.QueryRow(ctx, `UPDATE programs SET name=$2,short_name=$3,faculty=$4,degree_level=$5,start_year=$6,updated_by=$7,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ID, item.Name, item.ShortName, item.Faculty, item.DegreeLevel, item.StartYear, item.UpdatedBy)
	result, err := scanProgram(row)
	if err != nil {
		return domain.Program{}, translateConstraint(translateNotFound(err, apperror.ProgramNotFound), apperror.ProgramAlreadyExists)
	}
	err = r.event(ctx, "catalog.program.updated", "program", result.ID, item.UpdatedBy, result)

	return result, err
}

// DeleteProgram выполняет soft delete программы.
func (r *Postgres) DeleteProgram(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "programs", "program", id, actorID)
}

// ChangeProgramStatus меняет status программы и пишет outbox event.
func (r *Postgres) ChangeProgramStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Program, error) {
	result, err := scanProgram(r.pool.QueryRow(ctx, `UPDATE programs SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,university_id,name,short_name,faculty,degree_level,start_year,status,created_by,updated_by,created_at,updated_at,deleted_at`, id, status, actorID))
	if err != nil {
		return domain.Program{}, translateConstraint(translateNotFound(err, apperror.ProgramNotFound), apperror.ProgramAlreadyExists)
	}
	err = r.event(ctx, "catalog.program.updated", "program", id, actorID, result)

	return result, err
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

	row := r.pool.QueryRow(ctx, `INSERT INTO courses (program_id,name,slug,description,semester,year_number,status,created_by,updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ProgramID, item.Name, item.Slug, item.Description, item.Semester, item.YearNumber, item.Status, item.CreatedBy)
	result, err := scanCourse(row)
	if err != nil {
		return domain.Course{}, translateConstraint(err, apperror.CourseAlreadyExists)
	}
	err = r.event(ctx, "catalog.course.created", "course", result.ID, item.CreatedBy, result)

	return result, err
}

// GetCourse возвращает активный курс по ID.
func (r *Postgres) GetCourse(ctx context.Context, id string) (domain.Course, error) {
	item, err := scanCourse(r.pool.QueryRow(ctx, `SELECT id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at FROM courses WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.CourseNotFound)
}

// ListCourses возвращает активные курсы parent program или общий список.
func (r *Postgres) ListCourses(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Course, error) {
	query := `SELECT id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at FROM courses WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) != "" {
		query = `SELECT id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at FROM courses WHERE program_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR name ILIKE '%'||$3||'%') ORDER BY name LIMIT $4 OFFSET $5`
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
	result, err := scanCourse(r.pool.QueryRow(ctx, `UPDATE courses SET name=$2,slug=$3,description=$4,semester=$5,year_number=$6,updated_by=$7,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ID, item.Name, item.Slug, item.Description, item.Semester, item.YearNumber, item.UpdatedBy))
	if err != nil {
		return domain.Course{}, translateConstraint(translateNotFound(err, apperror.CourseNotFound), apperror.CourseAlreadyExists)
	}
	err = r.event(ctx, "catalog.course.updated", "course", result.ID, item.UpdatedBy, result)

	return result, err
}

// DeleteCourse выполняет soft delete курса.
func (r *Postgres) DeleteCourse(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "courses", "course", id, actorID)
}

// ChangeCourseStatus меняет status курса и пишет outbox event.
func (r *Postgres) ChangeCourseStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Course, error) {
	result, err := scanCourse(r.pool.QueryRow(ctx, `UPDATE courses SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,program_id,name,slug,description,semester,year_number,status,created_by,updated_by,created_at,updated_at,deleted_at`, id, status, actorID))
	if err != nil {
		return domain.Course{}, translateConstraint(translateNotFound(err, apperror.CourseNotFound), apperror.CourseAlreadyExists)
	}
	err = r.event(ctx, "catalog.course.updated", "course", id, actorID, result)

	return result, err
}

// CreateTopic создаёт тему, если parent course существует или не задан.
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

	row := r.pool.QueryRow(ctx, `INSERT INTO topics (course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.CourseID, item.ParentTopicID, item.Title, item.Slug, item.Description, item.OrderIndex, item.Difficulty, item.Status, item.CreatedBy)
	result, err := scanTopic(row)
	if err != nil {
		return domain.Topic{}, translateConstraint(err, apperror.TopicAlreadyExists)
	}
	err = r.event(ctx, "catalog.topic.created", "topic", result.ID, item.CreatedBy, result)

	return result, err
}

// GetTopic возвращает активную тему по ID.
func (r *Postgres) GetTopic(ctx context.Context, id string) (domain.Topic, error) {
	item, err := scanTopic(r.pool.QueryRow(ctx, `SELECT id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at FROM topics WHERE id=$1 AND deleted_at IS NULL`, id))

	return item, translateNotFound(err, apperror.TopicNotFound)
}

// ListTopics возвращает активные темы parent course или общий список.
func (r *Postgres) ListTopics(ctx context.Context, parentID string, options domain.ListOptions) ([]domain.Topic, error) {
	query := `SELECT id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at FROM topics WHERE deleted_at IS NULL AND ($1='' OR status::text=$1) AND ($2='' OR title ILIKE '%'||$2||'%') ORDER BY order_index,title LIMIT $3 OFFSET $4`
	args := []any{options.Status, options.Search, options.Limit, options.Offset}
	if strings.TrimSpace(parentID) != "" {
		query = `SELECT id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at FROM topics WHERE course_id=$1::uuid AND deleted_at IS NULL AND ($2='' OR status::text=$2) AND ($3='' OR title ILIKE '%'||$3||'%') ORDER BY order_index,title LIMIT $4 OFFSET $5`
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
	result, err := scanTopic(r.pool.QueryRow(ctx, `UPDATE topics SET parent_topic_id=$2,title=$3,slug=$4,description=$5,order_index=$6,difficulty=$7,updated_by=$8,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at`, item.ID, item.ParentTopicID, item.Title, item.Slug, item.Description, item.OrderIndex, item.Difficulty, item.UpdatedBy))
	if err != nil {
		return domain.Topic{}, translateConstraint(translateNotFound(err, apperror.TopicNotFound), apperror.TopicAlreadyExists)
	}
	err = r.event(ctx, "catalog.topic.updated", "topic", result.ID, item.UpdatedBy, result)

	return result, err
}

// DeleteTopic выполняет soft delete темы.
func (r *Postgres) DeleteTopic(ctx context.Context, id, actorID string) error {
	return r.softDelete(ctx, "topics", "topic", id, actorID)
}

// ChangeTopicStatus меняет status темы и пишет outbox event.
func (r *Postgres) ChangeTopicStatus(ctx context.Context, id string, status domain.Status, actorID string) (domain.Topic, error) {
	result, err := scanTopic(r.pool.QueryRow(ctx, `UPDATE topics SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,course_id,parent_topic_id,title,slug,description,order_index,difficulty,status,created_by,updated_by,created_at,updated_at,deleted_at`, id, status, actorID))
	if err != nil {
		return domain.Topic{}, translateConstraint(translateNotFound(err, apperror.TopicNotFound), apperror.TopicAlreadyExists)
	}
	err = r.event(ctx, "catalog.topic.updated", "topic", id, actorID, result)

	return result, err
}

// AddPrerequisite создаёт связь prerequisite и пишет outbox event.
func (r *Postgres) AddPrerequisite(ctx context.Context, item domain.TopicPrerequisite, actorID string) (domain.TopicPrerequisite, error) {
	err := r.pool.QueryRow(ctx, `INSERT INTO topic_prerequisites (topic_id,prerequisite_topic_id) VALUES ($1,$2) RETURNING topic_id,prerequisite_topic_id,created_at`, item.TopicID, item.PrerequisiteTopicID).Scan(&item.TopicID, &item.PrerequisiteTopicID, &item.CreatedAt)
	if err != nil {
		return domain.TopicPrerequisite{}, translateConstraint(err, apperror.InvalidTopicPrerequisite)
	}
	err = r.event(ctx, "catalog.topic.prerequisite.added", "topic", item.TopicID, actorID, item)

	return item, err
}

// RemovePrerequisite удаляет связь prerequisite и пишет outbox event.
func (r *Postgres) RemovePrerequisite(ctx context.Context, topicID, prerequisiteID, actorID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM topic_prerequisites WHERE topic_id=$1 AND prerequisite_topic_id=$2`, topicID, prerequisiteID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperror.New(apperror.InvalidTopicPrerequisite, "пререквизит не найден", 404)
	}

	return r.event(ctx, "catalog.topic.prerequisite.removed", "topic", topicID, actorID, map[string]string{"prerequisite_topic_id": prerequisiteID})
}

// ListPrerequisites возвращает prerequisites темы.
func (r *Postgres) ListPrerequisites(ctx context.Context, topicID string) ([]domain.TopicPrerequisite, error) {
	rows, err := r.pool.Query(ctx, `SELECT topic_id,prerequisite_topic_id,created_at FROM topic_prerequisites WHERE topic_id=$1 ORDER BY created_at`, topicID)
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

// PrerequisitePathExists проверяет достижимость темы в graph prerequisites.
func (r *Postgres) PrerequisitePathExists(ctx context.Context, fromID, toID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `WITH RECURSIVE graph(id) AS (SELECT prerequisite_topic_id FROM topic_prerequisites WHERE topic_id=$1 UNION SELECT tp.prerequisite_topic_id FROM topic_prerequisites tp JOIN graph g ON tp.topic_id=g.id) SELECT EXISTS(SELECT 1 FROM graph WHERE id=$2)`, fromID, toID).Scan(&exists)

	return exists, err
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
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM universities WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)

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
	if err := r.pool.QueryRow(ctx, `SELECT university_id FROM programs WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
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
	if err := r.pool.QueryRow(ctx, `SELECT program_id FROM courses WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
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
	if err := r.pool.QueryRow(ctx, `SELECT course_id FROM topics WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
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
	tag, err := r.pool.Exec(ctx, query, id, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperror.New(code, "объект не найден", 404)
	}

	return r.event(ctx, "catalog."+aggregate+".deleted", aggregate, id, actorID, map[string]string{"id": id})
}

// event записывает outbox event для будущего publisher worker.
func (r *Postgres) event(ctx context.Context, eventType, aggregateType, aggregateID, actorID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO outbox_events (event_type,actor_user_id,aggregate_id,aggregate_type,payload) VALUES ($1,$2,$3,$4,$5)`, eventType, actorID, aggregateID, aggregateType, data)

	return err
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
	err := row.Scan(&item.ID, &item.CourseID, &item.ParentTopicID, &item.Title, &item.Slug, &item.Description, &item.OrderIndex, &item.Difficulty, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)

	return item, err
}

// translateNotFound преобразует pgx.ErrNoRows в публичную not-found ошибку Ironhide.
func translateNotFound(err error, code string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(code, "объект не найден", 404)
	}

	return err
}

// translateConstraint преобразует PostgreSQL constraint violations в публичную ошибку Ironhide.
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
