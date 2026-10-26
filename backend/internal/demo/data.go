package demo

import "github.com/DarkApofis/tablero/backend/internal/domain"

// The demo's content. Its people and their passwords are public: they are
// documented in the README ("Datos de demo").

// Person is a demo user.
type Person struct {
	Key      string // how tasks refer to them
	Email    string
	Name     string
	Password string
	Role     domain.Role
}

var People = []Person{
	{Key: "ana", Email: "ana@estudionube.example", Name: "Ana Torres", Password: "nube-admin-demo", Role: domain.RoleAdmin},
	{Key: "bruno", Email: "bruno@estudionube.example", Name: "Bruno Díaz", Password: "nube-miembro-demo", Role: domain.RoleMember},
	{Key: "vera", Email: "vera@estudionube.example", Name: "Vera Molina", Password: "nube-viewer-demo", Role: domain.RoleViewer},
}

const (
	WorkspaceName = "Estudio Nube"
	BoardName     = "Rediseño web · Panadería Trigo"

	// GuestEmailSuffix marks guest accounts, which the reset deletes once
	// they are older than the guest TTL.
	GuestEmailSuffix = "@invitados.estudionube.example"
)

var BoardColumns = []string{"Pendiente", "En progreso", "En revisión", "Hecho"}

type labelSpec struct{ Name, Color string }

var BoardLabels = []labelSpec{
	{"Diseño", "#8b5cf6"},
	{"Frontend", "#2563eb"},
	{"Backend", "#059669"},
	{"Contenido", "#d97706"},
	{"Bug", "#dc2626"},
	{"Cliente", "#db2777"},
}

// taskSpec is a task and its history: created by By in Path[0], then moved
// through the rest of Path; Edit, if any, is a later change by EditBy.
type taskSpec struct {
	Title       string
	Description string
	Path        []string // column names
	Priority    domain.Priority
	Due         *int // days from today; nil: no date
	Assignee    string
	Labels      []string
	By          string

	Edit   *taskEdit
	EditBy string
}

type taskEdit struct {
	Priority *domain.Priority
	Assignee *string
	Archive  bool
}

func days(n int) *int                         { return &n }
func prio(p domain.Priority) *domain.Priority { return &p }
func who(key string) *string                  { return &key }

const (
	todo   = "Pendiente"
	doing  = "En progreso"
	review = "En revisión"
	done   = "Hecho"
)

var BoardTasks = []taskSpec{
	{Title: "Reunión de arranque con Panadería Trigo", Description: "Objetivos, plazos y quién aprueba cada entrega. Acta en la carpeta del proyecto.",
		Path: []string{todo, doing, done}, Priority: domain.PriorityHigh, Due: days(-12), Assignee: "ana", Labels: []string{"Cliente"}, By: "ana"},
	{Title: "Auditoría de la web actual", Description: "Velocidad, accesibilidad y qué páginas reciben visitas. Resumen de 1 página para el cliente.",
		Path: []string{todo, doing, done}, Priority: domain.PriorityMedium, Due: days(-9), Assignee: "bruno", Labels: []string{"Frontend"}, By: "ana"},
	{Title: "Moodboard y referencias", Description: "Panaderías de barrio, tonos cálidos, fotografía de producto con luz natural.",
		Path: []string{todo, doing, done}, Priority: domain.PriorityMedium, Due: days(-7), Assignee: "ana", Labels: []string{"Diseño"}, By: "ana"},
	{Title: "Aprobar paleta de color y tipografías", Description: "Dos propuestas enviadas. Falta la confirmación por escrito de Vera.",
		Path: []string{todo, review}, Priority: domain.PriorityHigh, Due: days(1), Assignee: "vera", Labels: []string{"Diseño", "Cliente"}, By: "ana"},
	{Title: "Wireframes de la home", Description: "Hero con el pan del día, horario, cómo llegar y pedidos para recoger.",
		Path: []string{todo, doing, review}, Priority: domain.PriorityHigh, Due: days(2), Assignee: "ana", Labels: []string{"Diseño"}, By: "ana"},
	{Title: "Sesión de fotos de producto", Description: "Reservada la mañana del jueves en el obrador. Lista de piezas: hogazas, bollería, tartas.",
		Path: []string{todo}, Priority: domain.PriorityMedium, Due: days(6), Assignee: "ana", Labels: []string{"Contenido", "Cliente"}, By: "ana"},
	{Title: "Textos de la página «Nuestra historia»", Description: "Entrevista a la familia fundadora; 400 palabras como máximo.",
		Path: []string{todo, doing}, Priority: domain.PriorityMedium, Due: days(4), Assignee: "bruno", Labels: []string{"Contenido"}, By: "ana"},
	{Title: "Componente de carta de productos", Description: "Tarjeta con foto, precio y alérgenos. Debe funcionar con 2 columnas en móvil.",
		Path: []string{todo, doing}, Priority: domain.PriorityHigh, Due: days(3), Assignee: "bruno", Labels: []string{"Frontend"}, By: "bruno"},
	{Title: "Formulario de pedidos para recoger", Description: "Elegir día y franja; confirmación por correo. Sin pagos online en esta fase.",
		Path: []string{todo}, Priority: domain.PriorityHigh, Due: days(9), Assignee: "bruno", Labels: []string{"Frontend", "Backend"}, By: "bruno"},
	{Title: "API de pedidos y franjas horarias", Description: "Límite de pedidos por franja configurable desde el panel.",
		Path: []string{todo, doing}, Priority: domain.PriorityHigh, Due: days(5), Assignee: "bruno", Labels: []string{"Backend"}, By: "bruno"},
	{Title: "Correo de confirmación de pedido", Description: "Plantilla con el logo y el resumen del pedido.",
		Path: []string{todo}, Priority: domain.PriorityLow, Due: days(12), Assignee: "bruno", Labels: []string{"Backend", "Contenido"}, By: "bruno"},
	{Title: "El menú móvil no se cierra al elegir una opción", Description: "Pasa en Safari iOS. Al navegar, el panel queda abierto encima del contenido.",
		Path: []string{todo, doing, review}, Priority: domain.PriorityMedium, Due: days(0), Assignee: "bruno", Labels: []string{"Bug", "Frontend"}, By: "bruno",
		Edit: &taskEdit{Priority: prio(domain.PriorityUrgent)}, EditBy: "ana"},
	{Title: "Horario de festivos en el pie de página", Description: "Que el cliente lo pueda cambiar sin tocar código.",
		Path: []string{todo}, Priority: domain.PriorityMedium, Due: days(8), Labels: []string{"Contenido"}, By: "ana",
		Edit: &taskEdit{Assignee: who("bruno")}, EditBy: "bruno"},
	{Title: "Optimizar imágenes (AVIF + tamaños)", Description: "Objetivo: menos de 200 KB en la home sin perder calidad.",
		Path: []string{todo}, Priority: domain.PriorityMedium, Assignee: "bruno", Labels: []string{"Frontend"}, By: "bruno"},
	{Title: "Mapa y cómo llegar", Description: "Mapa estático con enlace a la app de mapas; sin cookies de terceros.",
		Path: []string{todo, doing, done}, Priority: domain.PriorityLow, Due: days(-3), Assignee: "bruno", Labels: []string{"Frontend"}, By: "bruno"},
	{Title: "Configurar dominio y certificados", Description: "panaderiatrigo.example con HTTPS y redirección desde www.",
		Path: []string{todo, done}, Priority: domain.PriorityMedium, Due: days(-5), Assignee: "bruno", Labels: []string{"Backend"}, By: "ana"},
	{Title: "Revisión de accesibilidad (contraste y teclado)", Description: "Comprobar con lector de pantalla los flujos de pedido.",
		Path: []string{todo}, Priority: domain.PriorityHigh, Due: days(10), Assignee: "ana", Labels: []string{"Diseño", "Frontend"}, By: "ana"},
	{Title: "Presentación de la propuesta al cliente", Description: "Demo navegable de la home y la carta. 30 minutos, en el obrador.",
		Path: []string{todo}, Priority: domain.PriorityUrgent, Due: days(7), Assignee: "ana", Labels: []string{"Cliente"}, By: "ana"},
	{Title: "Política de privacidad y aviso legal", Description: "Pedir los datos fiscales al cliente.",
		Path: []string{todo}, Priority: domain.PriorityLow, Due: days(14), Assignee: "vera", Labels: []string{"Contenido", "Cliente"}, By: "ana"},
	{Title: "Error 500 al enviar el formulario de contacto", Description: "Solo con caracteres especiales en el nombre (ñ, tildes). Reproducido en staging.",
		Path: []string{todo, doing}, Priority: domain.PriorityHigh, Due: days(-1), Assignee: "bruno", Labels: []string{"Bug", "Backend"}, By: "ana"},
	{Title: "Logo en versión antigua", Description: "Sustituido por la versión nueva aprobada.",
		Path: []string{todo}, Priority: domain.PriorityLow, Labels: []string{"Diseño"}, By: "ana",
		Edit: &taskEdit{Archive: true}, EditBy: "ana"},
}
