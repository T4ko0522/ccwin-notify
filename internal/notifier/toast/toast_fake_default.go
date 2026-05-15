//go:build !windows || faketoast

package toast

// NewDefaultToaster は build tag に応じたデフォルト Toaster を返す。
// !windows または faketoast タグ時: FakeToaster を返す (CI / テスト用)。
// silent パラメータは Fake では使われないが、シグネチャを Windows 実装と
// 揃えることで呼び出し側 (orchestrator) の build tag 分岐をなくす。
func NewDefaultToaster(_ bool) Toaster {
	return NewFakeToaster()
}
