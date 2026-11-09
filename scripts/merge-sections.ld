/* Merge per-function/data sections after garbage collection so ELF does not
 * require extended section indices, which Bao does not accept. */
SECTIONS {
 .text 0 : SUBALIGN(64) { *(.text .text.* .gnu.linkonce.t.*) }
 .rodata 0 : { *(.rodata .rodata.* .gnu.linkonce.r.*) }
 .data.rel.ro 0 : { *(.data.rel.ro .data.rel.ro.*) }
 .data 0 : { *(.data .data.* .gnu.linkonce.d.*) }
 .bss 0 : { __bss_start = .; *(.bss .bss.* .gnu.linkonce.b.*) *(COMMON) }
 .tdata 0 : { *(.tdata .tdata.* .gnu.linkonce.td.*) }
 .tbss 0 : { *(.tbss .tbss.* .gnu.linkonce.tb.*) }
 .gcc_except_table 0 : { *(.gcc_except_table .gcc_except_table.*) }
 .init_array 0 : { KEEP(*(SORT_BY_INIT_PRIORITY(.init_array.*))) KEEP(*(.init_array)) }
 .fini_array 0 : { KEEP(*(SORT_BY_INIT_PRIORITY(.fini_array.*))) KEEP(*(.fini_array)) }
}
