#!/usr/bin/env sh

#DC_FILE=networks/rotate/docker-compose.yml


echo "passed: $1"

if [ "$1" == "r" ];
then
#	make e2e/app/compile
	docker-compose --ansi=never -f networks/rotate/docker-compose.yml stop validator03
	vpath=/Users/dmitrygolubev/go/src/github.com/dashevo/tenderdash/test/e2e/networks/dashcore
	rm -rf $vpath/validator03/data
	cp -R $vpath/../data $vpath/validator03/data
fi

docker-compose --ansi=never -f networks/rotate/docker-compose.yml up validator03

#cd networks/dashcore && docker-compose -f docker-compose.yml up validator03
