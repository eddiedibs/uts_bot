#!/home/edd1e/scripts/projs/uts_bot/uts_bot_env/bin/python3
import configuration as conf

from time import sleep
import os
import logging



from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from dotenv import load_dotenv
load_dotenv()
logging.basicConfig(
    level=logging.INFO, 
    format='%(asctime)s [%(levelname)s|%(name)s|%(funcName)s]:: %(message)s', 
    handlers=[logging.StreamHandler()]
)
class Helpers:


    def __init__(self, driver):
        self.driver = driver


    def get_saia_activities(self, element:object):
        self.driver.click_button(element.find_element(By.TAG_NAME, "a"))
        course_sections_len = len(self.driver.find_inner_element(By.CLASS_NAME,"course-content-item-content", is_single_element=False))
        for i in range(1, course_sections_len):
            course_sections = self.driver.find_inner_element(By.CLASS_NAME,"course-content-item-content", is_single_element=False)
            course_classes = course_sections[i].get_attribute("class")
            # check if the "collapse show" class is present in the list of classes
            if "collapse show" in course_classes:
                # the "collapse show" class is present
                activities_len = len(course_sections[i].find_elements(By.CLASS_NAME, "aalink.stretched-link"))
                for index in range(activities_len):
                    course_sections = self.driver.find_inner_element(By.CLASS_NAME,"course-content-item-content", is_single_element=False)
                    activity_title = course_sections[i].find_elements(By.CLASS_NAME,"text-uppercase.small")
                    activities = course_sections[i].find_elements(By.CLASS_NAME, "aalink.stretched-link")
                    
                    if activity_title[index].text.strip() in conf.UNDESIRED_ACTIVITIES:
                        pass

                    else:
                        activity_found = self.driver.click_button(activities[index])
                        if activity_found:
                            sleep(1)
                            self.driver.go_back()
                        else:
                            logging.error(f"ELEMENT:: '{activities[index].text}' was not found")
                            exit()
            else:
                # the "collapse show" class is not present
                course_headers = self.driver.find_inner_element(By.CLASS_NAME,"course-section-header", is_single_element=False)
                self.driver.click_button(course_headers[i].find_element(By.TAG_NAME, "a"))
                # the "collapse show" class is present
                activities_len = len(course_sections[i].find_elements(By.CLASS_NAME, "aalink.stretched-link"))
                for index in range(activities_len):
                    course_sections = self.driver.find_inner_element(By.CLASS_NAME,"course-content-item-content", is_single_element=False)
                    activity_title = course_sections[i].find_elements(By.CLASS_NAME,"text-uppercase.small")
                    activities = course_sections[i].find_elements(By.CLASS_NAME, "aalink.stretched-link")
                    if activity_title[i].text.upper().strip() in conf.UNDESIRED_ACTIVITIES:
                        pass

                    else:
                        activity_found = self.driver.click_button(activities[index])
                        if activity_found:
                            sleep(1)
                            self.driver.go_back()
                        else:
                            logging.error(f"ELEMENT:: '{activities[index].text}' was not found")
                            exit()


if __name__ == "__main__":
    pass