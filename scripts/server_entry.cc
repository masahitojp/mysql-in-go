// mysqld_main is the server entry point defined in sql/mysqld.cc.
extern int mysqld_main(int argc, char **argv);
extern "C" int mysqlmem_server_main(int argc, char **argv) {
  return mysqld_main(argc, argv);
}
