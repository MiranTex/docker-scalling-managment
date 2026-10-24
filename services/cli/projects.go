package main

import "fmt"

func cmdProjects(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := newClient(cfg)
	var projects []struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := c.call("GET", "/v1/projects", nil, &projects); err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Println("Ainda não és membro de nenhum projeto.")
	}
	for _, p := range projects {
		var detail struct {
			MyRole       string `json:"myRole"`
			Environments []struct {
				Slug string `json:"slug"`
			} `json:"environments"`
		}
		if err := c.call("GET", "/v1/projects/"+p.Slug, nil, &detail); err != nil {
			return err
		}
		fmt.Printf("%s (%s) -- papel: %s\n", p.Slug, p.Name, detail.MyRole)
		for _, e := range detail.Environments {
			var services, addons []struct {
				Slug string `json:"slug"`
			}
			path := target{project: p.Slug, env: e.Slug}.envPath
			_ = c.call("GET", path("services"), nil, &services)
			_ = c.call("GET", path("addons"), nil, &addons)
			fmt.Printf("  %s/%s\n", p.Slug, e.Slug)
			for _, s := range services {
				fmt.Printf("    %s/%s/%s  (serviço)\n", p.Slug, e.Slug, s.Slug)
			}
			for _, a := range addons {
				fmt.Printf("    %s/%s/%s  (addon)\n", p.Slug, e.Slug, a.Slug)
			}
		}
	}
	return nil
}
