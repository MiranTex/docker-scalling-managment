package store

// Roles conhecidas, sem hierarquia implícita no código -- cada endpoint
// admin decide explicitamente que role(s) aceita. Isto é um gate GROSSO
// (a que superfícies administrativas uma conta tem acesso); autorização
// mais fina (ex: que ações específicas dentro do autoscaler) é
// responsabilidade de cada serviço de recursos, que nem precisa saber
// que estas constantes existem -- só lê a claim "role" do JWT que validar.
const (
	RoleUser       = "user"        // default -- só gere as próprias coisas (ex: os seus API tokens)
	RoleAdmin      = "admin"       // gestão da aplicação
	RoleInfraAdmin = "infra-admin" // gestão de infraestrutura (ex: autoscaler)
	RoleSuperAdmin = "super-admin" // único que pode gerir outras contas: criar, listar, atribuir role
)

// ValidRole confirma que role é uma das constantes acima -- usado sempre
// que uma role vem de um pedido HTTP, nunca confiando em texto livre do
// cliente (a CHECK constraint na base de dados é a segunda linha de
// defesa, não a primeira).
func ValidRole(role string) bool {
	switch role {
	case RoleUser, RoleAdmin, RoleInfraAdmin, RoleSuperAdmin:
		return true
	default:
		return false
	}
}
