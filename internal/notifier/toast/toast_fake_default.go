//go:build !windows || faketoast

package toast

// NewDefaultToaster は build tag に応じたデフォルト Toaster を返す。
// !windows または faketoast タグ時: FakeToaster を返す (CI / テスト用)。
func NewDefaultToaster() Toaster {
	return NewFakeToaster()
}
