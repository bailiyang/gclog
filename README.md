# gclog
gclog是一个go日志管理库，拥有以下特点
- 基于go标准库的log创建，不需要额外的库文件
- 方便的更改标准输出/日志文件输出，方便调试、运行
- 更多的日志级别可选
- 在打印日志的同时，自动切分日志、删除过期日志文件

# Use
由于是个小项目，没写test文件
具体使用见go Doc以及注释
本身非常简单

# TODO
缺少创建日志文件时，递归创建目录的功能
