# ビルドの仕組みと互換性の調整

通常ビルドの成功だけでは Bao 版の成功にならないため、生成・Go リンク・実行の各段階で確認した。
以下は `make bootstrap` と `make package` に反映済み。

- 同梱 Abseil と Protobuf を静的ライブラリにし、TLS を global-dynamic にする。
- C++ / GCC ランタイムと glibc の `libc_nonshared.a` を部分リンクする。
- 約 19 万セクションをまとめ、Bao の拡張セクション番号非対応を回避する。
- 関数ごとの入力セクションを 64 バイト境界に配置し、Go の findfunc テーブルの密度上限を回避する。
- Bao が非対応の `.note.gnu.property` を除く。この実験では GNU property note を保持しない。
- `scripts/bao-gcc15.patch` で、GCC 15 の `cmpq $0, weak@GOTPCREL(%rip)` を Bao が正しく扱えるようにする。対応する正常・異常系テストを追加し、`go test ./linuxelf -run TestAMD64` に成功。
- 条件変数の 6 関数には Go の dynamic import で `GLIBC_2.3.2` を明示する。無指定では旧 ABI が選ばれ、InnoDB の初期化で失敗した。この生成後の変更は `native/mysqlmem-glibc-versions.json` に記録する。

`make build`、`make vet`、`make test` で最終確認した。
Bao 生成物に MySQL、GCC、glibc のライセンス通知を同梱する。
サーバー全機能や長時間運用の検証は行っていない。追加の SQL 検証結果は [動作確認](verification.md)を参照。

