package store

// Roles conhecidas. Sem hierarquia entre as demais -- cada endpoint admin
// decide explicitamente que role(s) aceita -- EXCETO super-admin, que
// funciona como bypass universal: todo requireRole (aqui e em qualquer
// outro serviço de recursos, ex: dbadmin, secretsadmin, a API admin do
// autoscaler) aceita super-admin além da role especificamente exigida.
// Isto é um gate GROSSO (a que superfícies administrativas uma conta tem
// acesso); autorização mais fina (ex: que ações específicas dentro do
// autoscaler) é responsabilidade de cada serviço de recursos, que nem
// precisa saber que estas constantes existem -- só lê a claim "role" do
// JWT que validar (e sabe reconhecer literalmente "super-admin" para o
// bypass).
const (
	RoleUser       = "user"        // default -- só gere as próprias coisas (ex: os seus API tokens)
	RoleAdmin      = "admin"       // gestão da aplicação
	RoleInfraAdmin = "infra-admin" // gestão de infraestrutura (ex: autoscaler)
	RoleSuperAdmin = "super-admin" // único que pode gerir outras contas: criar, listar, atribuir role
	// RoleService identifica uma conta de MÁQUINA, não humana -- ex: o
	// processo cmd/group do autoscaler, que precisa de ler valores de
	// segredos no secretsadmin sem nenhuma sessão de utilizador por
	// trás. Nunca tem password_credentials (não faz login por
	// email/password); só recebe tokens via
	// POST /v1/admin/users/{id}/tokens, emitido por um super-admin.
	RoleService = "service"
)

// ValidRole confirma que role é uma das constantes acima -- usado sempre
// que uma role vem de um pedido HTTP, nunca confiando em texto livre do
// cliente (a CHECK constraint na base de dados é a segunda linha de
// defesa, não a primeira).
func ValidRole(role string) bool {
	switch role {
	case RoleUser, RoleAdmin, RoleInfraAdmin, RoleSuperAdmin, RoleService:
		return true
	default:
		return false
	}
}
