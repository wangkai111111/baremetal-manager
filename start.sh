
docker compose  down
for i in $(docker volume  ls| grep baremetal | awk '{print $2}');do docker volume  rm $i ;done


docker compose build
docker compose up -d
