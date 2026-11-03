package main

type MortgageMonth struct {
	Interest  float64
	Principal float64
	Remaining float64
}

func calculateMortgageMonth(balance, annualRate, payment, extraPayment float64) MortgageMonth {
	interest := (balance * annualRate) / (100 * 12)
	principal := payment - interest + extraPayment
	remaining := balance - principal

	return MortgageMonth{
		Interest:  interest,
		Principal: principal,
		Remaining: remaining,
	}
}

func calculateMortgageSchedule(balance, annualRate, payment, extraPayment float64, months int) []MortgageMonth {
	schedule := make([]MortgageMonth, 0, months)

	for i := 0; i < months; i++ {
		result := calculateMortgageMonth(balance, annualRate, payment, extraPayment)
		schedule = append(schedule, result)
		balance = result.Remaining
	}

	return schedule
}
