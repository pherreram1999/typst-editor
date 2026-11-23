// Nombre de los invitados (enlaces #/i/…): uno aleatorio («Colibrí curioso»)
// o el que escriban. La preferencia se guarda en localStorage y se reutiliza
// en cualquier enlace de invitado abierto en este navegador.
const KEY = 'typsteditor.guestName'

// [animal, género] y adjetivos [masculino, femenino].
const ANIMALS = [
  ['Ajolote', 'm'], ['Colibrí', 'm'], ['Jaguar', 'm'], ['Quetzal', 'm'], ['Tlacuache', 'm'], ['Coyote', 'm'],
  ['Búho', 'm'], ['Delfín', 'm'], ['Lince', 'm'], ['Mapache', 'm'], ['Pingüino', 'm'], ['Zorro', 'm'],
  ['Tucán', 'm'], ['Castor', 'm'], ['Panda', 'm'], ['Flamenco', 'm'],
  ['Ballena', 'f'], ['Tortuga', 'f'], ['Nutria', 'f'], ['Ardilla', 'f'], ['Mariposa', 'f'], ['Liebre', 'f'],
  ['Garza', 'f'], ['Foca', 'f'], ['Iguana', 'f'], ['Cebra', 'f'], ['Jirafa', 'f'], ['Luciérnaga', 'f'],
]
const ADJECTIVES = [
  ['curioso', 'curiosa'], ['valiente', 'valiente'], ['sereno', 'serena'], ['veloz', 'veloz'], ['audaz', 'audaz'],
  ['sabio', 'sabia'], ['amable', 'amable'], ['atento', 'atenta'], ['inquieto', 'inquieta'], ['alegre', 'alegre'],
  ['astuto', 'astuta'], ['tranquilo', 'tranquila'], ['brillante', 'brillante'], ['risueño', 'risueña'],
  ['paciente', 'paciente'], ['creativo', 'creativa'], ['puntual', 'puntual'], ['ingenioso', 'ingeniosa'],
]

const pick = (a) => a[Math.floor(Math.random() * a.length)]

export function randomGuestName() {
  const [animal, g] = pick(ANIMALS)
  return `${animal} ${pick(ADJECTIVES)[g === 'm' ? 0 : 1]}`
}

// { name, random }: random indica que el nombre se generó (y se puede volver a sortear).
export function savedGuestName() {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? 'null')
    if (v && typeof v.name === 'string' && v.name.trim()) return { name: v.name.trim().slice(0, 40), random: !!v.random }
  } catch {
    /* sin almacenamiento o valor corrupto */
  }
  return null
}

export function saveGuestName(name, random) {
  try {
    localStorage.setItem(KEY, JSON.stringify({ name, random }))
  } catch {
    /* sin almacenamiento */
  }
}
