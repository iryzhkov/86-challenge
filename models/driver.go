package models

import (
	"context"
)

type Driver struct {
	ID            int
	Name          string
	CarClass      string
	CarGeneration string
	CarModel      string
	CarYear       int
	TireBrand     string
	TireModel     string
	ModPoints     int
}

func SearchDrivers(query string, limit int) ([]Driver, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT id, name, car_class, car_generation, car_model, COALESCE(car_year, 0),
		        COALESCE(tire_brand, ''), COALESCE(tire_model, ''), mod_points
		 FROM drivers
		 WHERE name ILIKE '%' || $1 || '%'
		 ORDER BY name
		 LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var drivers []Driver
	for rows.Next() {
		var d Driver
		if err := rows.Scan(&d.ID, &d.Name, &d.CarClass, &d.CarGeneration,
			&d.CarModel, &d.CarYear, &d.TireBrand, &d.TireModel, &d.ModPoints); err != nil {
			return nil, err
		}
		drivers = append(drivers, d)
	}
	return drivers, nil
}

func GetOrCreateDriver(name string) (int, error) {
	var id int
	err := DB.QueryRow(context.Background(),
		`INSERT INTO drivers (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`, name).Scan(&id)
	return id, err
}

func GetDriver(id int) (*Driver, error) {
	d := &Driver{}
	err := DB.QueryRow(context.Background(),
		`SELECT id, name, COALESCE(car_class, ''), COALESCE(car_generation, ''),
		        COALESCE(car_model, ''), COALESCE(car_year, 0),
		        COALESCE(tire_brand, ''), COALESCE(tire_model, ''), mod_points
		 FROM drivers WHERE id = $1`, id).Scan(
		&d.ID, &d.Name, &d.CarClass, &d.CarGeneration,
		&d.CarModel, &d.CarYear, &d.TireBrand, &d.TireModel, &d.ModPoints)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func UpdateDriverSetup(id int, carClass, generation, model, tireBrand, tireModel string, modPoints int) error {
	_, err := DB.Exec(context.Background(),
		`UPDATE drivers SET car_class = $2, car_generation = $3, car_model = $4,
		 tire_brand = $5, tire_model = $6, mod_points = $7
		 WHERE id = $1`,
		id, carClass, generation, model, tireBrand, tireModel, modPoints)
	return err
}
