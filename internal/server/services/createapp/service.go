package createapp

type ServiceDeps struct {
	DB interface{} // Placeholder for db.DB
	// Additional clients will be injected in subsequent tasks
}

func NewService(deps ServiceDeps) *Service {
	return &Service{
		db: deps.DB,
	}
}

type Service struct {
	db interface{}
}
