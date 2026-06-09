package handler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"com.tom-ludwig/go-server-template/internal/api/users"
	"com.tom-ludwig/go-server-template/internal/repository"
)

var tracer = otel.Tracer("handler")

// compile-time check
var _ users.StrictServerInterface = (*UserHandler)(nil)

type UserHandler struct {
	Queries *repository.Queries
}

func NewUserHandler(queries *repository.Queries) *UserHandler {
	return &UserHandler{
		Queries: queries,
	}
}

func (u *UserHandler) GetUser(ctx context.Context, request users.GetUserRequestObject) (users.GetUserResponseObject, error) {
	ctx, span := tracer.Start(ctx, "handler.GetUser")
	defer span.End()

	userUUID, err := uuid.Parse(request.Params.UserId)
	if err != nil {
		span.SetAttributes(attribute.String("user.id", request.Params.UserId))
		span.SetStatus(codes.Error, "invalid user id")
		return users.GetUser400JSONResponse{}, nil
	}

	span.SetAttributes(attribute.String("user.id", userUUID.String()))

	// user, err := u.Queries.GetUser(ctx, userUUID)
	// if err != nil {
	// 	if errors.Is(err, sql.ErrNoRows) {
	// 		return users.GetUser404JSONResponse{}, nil
	// 	}
	// 	span.RecordError(err)
	// 	span.SetStatus(codes.Error, err.Error())
	// 	return users.GetUser404JSONResponse{}, nil
	// }
	// return users.GetUser200JSONResponse{
	// 	UserId:    user.UserID.String(),
	// 	FirstName: user.FirstName.String,
	// 	LastName:  user.LastName.String,
	// 	Email:     user.Email.String,
	// }, nil

	return users.GetUser200JSONResponse{
		UserId:    userUUID.String(),
		FirstName: "Ada",
		LastName:  "Lovelace",
		Email:     "ada@example.com",
	}, nil
}

func (u *UserHandler) CreateUser(ctx context.Context, request users.CreateUserRequestObject) (users.CreateUserResponseObject, error) {
	ctx, span := tracer.Start(ctx, "handler.CreateUser")
	defer span.End()

	// newUser, err := u.Queries.CreateUser(ctx, repository.CreateUserParams{
	// 	FirstName: pgtype.Text{String: request.Body.FirstName, Valid: true},
	// 	LastName:  pgtype.Text{String: request.Body.LastName, Valid: true},
	// 	Email:     pgtype.Text{String: request.Body.Email, Valid: true},
	// })
	// if err != nil {
	// 	span.RecordError(err)
	// 	span.SetStatus(codes.Error, err.Error())
	// 	slog.Error(
	// 		"An error occurred while trying to create a user",
	// 		"Error: ", err,
	// 	)
	// 	return users.CreateUser500JSONResponse{}, nil
	// }
	// span.SetAttributes(attribute.String("user.id", newUser.UserID.String()))
	// return users.CreateUser201JSONResponse{
	// 	UserId:    newUser.UserID.String(),
	// 	FirstName: newUser.FirstName.String,
	// 	LastName:  newUser.LastName.String,
	// 	Email:     newUser.Email.String,
	// }, nil

	_ = request
	_ = pgtype.Text{}
	_ = repository.CreateUserParams{}
	_ = sql.ErrNoRows
	_ = errors.New("")
	_ = slog.Default()
	span.SetStatus(codes.Error, "db disabled")
	return users.CreateUser500JSONResponse{}, nil
}

func (u *UserHandler) GetUsers(ctx context.Context, request users.GetUsersRequestObject) (users.GetUsersResponseObject, error) {
	ctx, span := tracer.Start(ctx, "handler.GetUsers")
	defer span.End()

	// page := int32(1)
	// if request.Params.Page != nil {
	// 	page = int32(*request.Params.Page)
	// }
	// limit := int32(20)
	// if request.Params.Limit != nil {
	// 	limit = int32(*request.Params.Limit)
	// }
	// if page < 1 || limit < 1 || limit > 100 {
	// 	return users.GetUsers400JSONResponse{...}, nil
	// }
	// totalRecords, err := u.Queries.CountUsers(ctx)
	// ...
	// dbUsers, err := u.Queries.GetUsers(ctx, queryParameters)
	// ...

	_ = request
	span.SetStatus(codes.Error, "db disabled")
	return users.GetUsers500JSONResponse{
		InternalServerErrorJSONResponse: users.InternalServerErrorJSONResponse{
			Message: "database disabled",
		},
	}, nil
}
