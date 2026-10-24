package logging

import "unicode/utf8"

// MaxValueLength acota la longitud de un valor que viene de fuera (el mensaje
// de un panic, el nombre de un fichero, un identificador de correlación). Sin
// un tope, un cliente podría escribir megabytes en una línea de log.
const MaxValueLength = 256

// MaxStackLength acota la longitud de un stack de panic. El stack completo
// puede ocupar kilobytes: interesa el principio (dónde empezó), no la cola.
const MaxStackLength = 4096

// Truncate acota un valor a max runes y marca el recorte con "…" para que un
// lector sepa que la línea está incompleta. Un max de 0 o menos significa
// "sin límite" y devuelve el valor intacto.
//
// El control de caracteres no es responsabilidad de esta función: los handlers
// de slog (JSON y texto) escapan el valor al escribirlo.
func Truncate(value string, max int) string {
	if max <= 0 {
		return value
	}
	// El conteo evita copiar la cadena entera en un []rune cuando el valor
	// cabe: los valores que vienen de fuera pueden ser grandes.
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max]) + "…"
}
