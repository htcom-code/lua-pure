package luapure

// OpenLibs installs the standard libraries available by default (linit.c).
// openPackage runs early so the libraries opened after it register themselves
// in package.loaded.
func (L *LState) OpenLibs() {
	L.OpenBase()
	L.openPackage()
	L.OpenString()
	L.OpenTable()
	L.OpenMath()
	L.OpenOS()
	L.OpenIO()
	L.OpenDebug()
	L.OpenUTF8()
	L.OpenCoroutine()
}
