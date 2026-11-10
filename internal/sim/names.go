package sim

import (
	"fmt"
	"math/rand"

	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

var firstNames = []string{
	"Abdul", "Adriana", "Ahmed", "Aisha", "Alejandro", "Amadou", "Ana", "Andre", "Anil", "Arturo",
	"Bilal", "Brian", "Carlos", "Carmen", "Chen", "Daniel", "Darnell", "Diego", "Dmitri", "Elena",
	"Emeka", "Esther", "Fatima", "Fernando", "Gabriel", "Grace", "Gurpreet", "Hassan", "Hector", "Ibrahim",
	"Ines", "Ivan", "Jamal", "Jasmine", "Javier", "Jin", "Jorge", "Jose", "Juan", "Kareem",
	"Kevin", "Kwame", "Lakshmi", "Leila", "Liam", "Luis", "Malik", "Manuel", "Maria", "Mariam",
	"Marcus", "Mei", "Miguel", "Mohammed", "Nadia", "Nikolai", "Noor", "Olga", "Omar", "Oscar",
	"Pablo", "Priya", "Rafael", "Rahul", "Ramon", "Rashid", "Ricardo", "Rosa", "Samir", "Sandeep",
	"Santiago", "Sergei", "Sofia", "Sunil", "Tariq", "Tenzin", "Tomas", "Valentina", "Victor", "Wei",
	"Xavier", "Yasmin", "Yusuf", "Zainab", "Zhang", "Mamadou", "Kofi", "Rina", "Vikram", "Lucia",
}

var lastInitials = "ABCDEFGHJKLMNOPRSTVWYZ"

var cars = map[engine.Product][]string{
	engine.Standard: {"Toyota Camry", "Honda Accord", "Toyota Prius", "Nissan Altima", "Hyundai Sonata", "Kia K5", "Toyota Corolla", "Honda Civic"},
	engine.Comfort:  {"Toyota Camry Hybrid", "Tesla Model 3", "Lexus ES", "Honda Accord Hybrid", "Volvo S60", "Genesis G70"},
	engine.XL:       {"Toyota Sienna", "Honda Odyssey", "Chevrolet Suburban", "Toyota Highlander", "Kia Carnival", "Ford Explorer"},
}

var colors = []string{"Black", "Gray", "Silver", "White", "Blue", "Dark gray"}

func driverInfo(rng *rand.Rand, id string, p engine.Product) engine.DriverInfo {
	name := fmt.Sprintf("%s %c.", firstNames[rng.Intn(len(firstNames))], lastInitials[rng.Intn(len(lastInitials))])
	models := cars[p]
	return engine.DriverInfo{
		ID:        id,
		Name:      name,
		Car:       colors[rng.Intn(len(colors))] + " " + models[rng.Intn(len(models))],
		Plate:     fmt.Sprintf("T%06dC", rng.Intn(1000000)),
		Product:   p,
		Rating:    4.7 + float64(rng.Intn(30))/100,
		Simulated: true,
	}
}

func HumanDriverInfo(seed int64, id string, p engine.Product) engine.DriverInfo {
	info := driverInfo(rand.New(rand.NewSource(seed)), id, p)
	info.Simulated = false
	info.Rating = 5
	return info
}
