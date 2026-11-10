# Third-party sources and notices

The root MIT license covers the original Go programs, build/test scripts and
documentation in this repository. It does not relicense upstream code, patch
context, downloaded sources, generated native objects or linked binaries.

| Component | Location / use | Upstream license |
| --- | --- | --- |
| MySQL Server 8.4.13 | Downloaded to `.work/mysql`; `scripts/mysql-bao.patch` modifies its bundled Abseil build and TLS setting | MySQL's [LICENSE at the pinned revision](https://github.com/mysql/mysql-server/blob/8a180268d832300c09a375115dfa0ea77d83f1af/LICENSE), including component-specific licenses and exceptions |
| Bao | Downloaded to `.work/bao`; `scripts/bao-gcc15.patch` modifies its ELF translator and tests | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| Abseil | Upstream context in `scripts/mysql-bao.patch` | Apache-2.0; preserve the upstream notices in the fetched source |
| Go MySQL Driver | Go module dependency | [MPL-2.0](https://github.com/go-sql-driver/mysql/blob/v1.10.1/LICENSE) |
| filippo.io/edwards25519 | Go module dependency | [BSD-3-Clause](https://github.com/FiloSottile/edwards25519/blob/v1.2.0/LICENSE) |

`LICENSES/GPL-2.0-only.txt` preserves the GPL v2 text referenced by MySQL source
and its CMake patch context. The downloaded MySQL tree contains its complete
component notices; the short table above does not replace those notices.

`make source-archive` includes this repository's source and patches. It excludes
MySQL, Bao and all generated/downloaded files. `make package` copies MySQL, GCC
and glibc notice files to the generated package, alongside Bao's generated
bootstrap notices. Other linked dependencies retain the notices in their source
trees. The root MIT license is not a license for the combined executable.
Prebuilt server releases include a separate corresponding-source archive and
the linked runtime notices. See docs/distribution.md.
